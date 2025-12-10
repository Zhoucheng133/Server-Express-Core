use lazy_static::lazy_static;
use serde::Serialize;
use ssh2::{Session, Sftp};
use std::ffi::{CStr, CString};
use std::fs::{self, File};
use std::io::{Read, Write};
use std::net::TcpStream;
use std::os::raw::c_char;
use std::path::{Path};
use std::sync::Mutex;

struct SftpConnection {
    _tcp: TcpStream, 
    _session: Session,
    sftp: Sftp,
}


lazy_static! {
    static ref GLOBAL_SFTP: Mutex<Option<SftpConnection>> = Mutex::new(None);
}

#[derive(Serialize)]
struct FileInfo {
    #[serde(rename = "type")]
    file_type: String,
    name: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    size: Option<u64>,
}

fn c_str_to_string(ptr: *const c_char) -> String {
    if ptr.is_null() {
        return String::new();
    }
    unsafe {
        CStr::from_ptr(ptr)
            .to_string_lossy()
            .into_owned()
    }
}

fn return_string(s: String) -> *mut c_char {
    CString::new(s).unwrap().into_raw()
}

fn return_ok() -> *mut c_char {
    return_string("OK".to_string())
}

fn return_err(e: impl std::fmt::Display) -> *mut c_char {
    return_string(format!("ERR: {}", e))
}


// SSH登录 【✅】
#[no_mangle]
pub extern "C" fn SSHLogin(
    url: *const c_char,
    port: *const c_char,
    username: *const c_char,
    password: *const c_char,
) -> *mut c_char {
    let url = c_str_to_string(url);
    let port = c_str_to_string(port);
    let username = c_str_to_string(username);
    let password = c_str_to_string(password);

    let address = format!("{}:{}", url, port);

    // 1. 连接 TCP
    let tcp = match TcpStream::connect(&address) {
        Ok(t) => t,
        Err(e) => return return_err(format!("TCP connection failed: {}", e)),
    };

    // 2. 初始化 SSH Session
    let mut sess = match Session::new() {
        Ok(s) => s,
        Err(e) => return return_err(format!("Session creation failed: {}", e)),
    };

    sess.set_tcp_stream(tcp.try_clone().unwrap());

    if let Err(e) = sess.handshake() {
        return return_err(format!("Handshake failed: {}", e));
    }

    // 3. 密码认证
    if let Err(e) = sess.userauth_password(&username, &password) {
        return return_err(format!("Authentication failed: {}", e));
    }

    // 4. 初始化 SFTP
    let sftp = match sess.sftp() {
        Ok(s) => s,
        Err(e) => return return_err(format!("SFTP initialization failed: {}", e)),
    };

    // 5. 存入全局变量
    let mut global = GLOBAL_SFTP.lock().unwrap();
    *global = Some(SftpConnection {
        _tcp: tcp,
        _session: sess,
        sftp,
    });

    return_ok()
}

// SFTP 列表【✅】
#[no_mangle]
pub extern "C" fn SftpList(path: *const c_char) -> *mut c_char {
    let path_str = c_str_to_string(path);
    let global = GLOBAL_SFTP.lock().unwrap();

    if let Some(conn) = &*global {
        match conn.sftp.readdir(Path::new(&path_str)) {
            Ok(entries) => {
                let mut file_infos = Vec::new();
                for (path_buf, stat) in entries {
                    let name = path_buf
                        .file_name()
                        .and_then(|n| n.to_str())
                        .unwrap_or("")
                        .to_string();
                    
                    let f_type = if stat.is_dir() { "dir" } else { "file" };

                    let size = if stat.is_dir() {
                        None
                    } else {
                        Some(stat.size.unwrap_or(0))
                    };
                    
                    file_infos.push(FileInfo {
                        file_type: f_type.to_string(),
                        name,
                        size,
                    });
                }
                match serde_json::to_string(&file_infos) {
                    Ok(json) => return_string(json),
                    Err(e) => return_err(format!("JSON serialization failed: {}", e)),
                }
            }
            Err(e) => return_err(format!("List failed: {}", e)),
        }
    } else {
        return_err("Not connected")
    }
}

// SFTP 递归下载【✅】
fn download_recursive(sftp: &Sftp, remote_path: &Path, local_path: &Path) -> Result<(), String> {
    // 获取远程文件状态
    let stat = sftp.stat(remote_path).map_err(|e| e.to_string())?;

    if stat.is_dir() {
        // 如果是目录，在本地创建目录
        if !local_path.exists() {
            fs::create_dir_all(local_path).map_err(|e| e.to_string())?;
        }

        // 读取远程目录内容
        let entries = sftp.readdir(remote_path).map_err(|e| e.to_string())?;
        for (child_remote_path, _) in entries {
            let file_name = child_remote_path.file_name().unwrap();
            // 排除 . 和 ..
            if file_name == "." || file_name == ".." { continue; }
            
            let child_local_path = local_path.join(file_name);
            download_recursive(sftp, &child_remote_path, &child_local_path)?;
        }
    } else {
        if let Some(parent) = local_path.parent() {
            fs::create_dir_all(parent).map_err(|e| e.to_string())?;
        }

        let mut remote_file = sftp.open(remote_path).map_err(|e| e.to_string())?;
        let mut local_file = File::create(local_path).map_err(|e| e.to_string())?;

        // 使用大 buffer 手动循环读取
        let mut buffer = vec![0u8; 512 * 1024]; // 512KB buffer，可调大到 1MB
        loop {
            let n = remote_file.read(&mut buffer).map_err(|e| e.to_string())?;
            if n == 0 { break; }
            local_file.write_all(&buffer[..n]).map_err(|e| e.to_string())?;
        }
    }
    Ok(())
}

// SFTP 下载【✅】
#[no_mangle]
pub extern "C" fn SftpDownload(path: *const c_char, local: *const c_char) -> *mut c_char {
    let remote_path_str = c_str_to_string(path);
    let local_base_str = c_str_to_string(local);
    
    let global = GLOBAL_SFTP.lock().unwrap();
    if let Some(conn) = &*global {
        let remote_path = Path::new(&remote_path_str);
        let file_name = match remote_path.file_name() {
            Some(name) => name,
            None => return return_err("Invalid remote path"),
        };
        let target_local = Path::new(&local_base_str).join(file_name);

        match download_recursive(&conn.sftp, remote_path, &target_local) {
            Ok(_) => return_ok(),
            Err(e) => return_err(e),
        }
    } else {
        return_err("Not connected")
    }
}
// 确保目录存在
fn ensure_remote_dir(sftp: &Sftp, path: &Path) -> Result<(), String> {
    let mut cur = Path::new("/").to_path_buf();

    for comp in path.components() {
        if let std::path::Component::Normal(name) = comp {
            let name_str = name.to_string_lossy();
            cur = cur.join(&*name_str);

            // 尝试创建目录，不管是否已存在
            let _ = sftp.mkdir(&cur, 0o755);
        }
    }

    Ok(())
}

// SFTP 递归上传【✅】
fn upload_recursive(sftp: &Sftp, local_path: &Path, remote_path: &Path) -> Result<(), String> {
    if local_path.is_dir() {
        ensure_remote_dir(sftp, remote_path)?;

        for entry in fs::read_dir(local_path).map_err(|e| e.to_string())? {
            let entry = entry.map_err(|e| e.to_string())?;
            let child_local = entry.path();

            let child_name = child_local.file_name().ok_or("Invalid local path")?;
            let child_remote = remote_path.join(child_name);

            upload_recursive(sftp, &child_local, &child_remote)?;
        }
    } else {
        // 上传文件前确保远程父目录存在
        if let Some(parent) = remote_path.parent() {
            ensure_remote_dir(sftp, parent)?;
        }

        let mut local_file = File::open(local_path).map_err(|e| e.to_string())?;
        let mut remote_file = sftp.create(remote_path).map_err(|e| e.to_string())?;

        // 使用大 buffer 手动循环读取
        let mut buffer = vec![0u8; 512 * 1024]; // 512KB 或 1MB
        loop {
            let n = local_file.read(&mut buffer).map_err(|e| e.to_string())?;
            if n == 0 { break; }
            remote_file.write_all(&buffer[..n]).map_err(|e| e.to_string())?;
        }
    }
    Ok(())
}


// SFTP 上传【✅】
#[no_mangle]
pub extern "C" fn SftpUpload(path: *const c_char, local: *const c_char) -> *mut c_char {
    let remote_base_str = c_str_to_string(path);
    let local_path_str = c_str_to_string(local);

    let global = GLOBAL_SFTP.lock().unwrap();
    if let Some(conn) = &*global {
        let local_path = Path::new(&local_path_str);

        let remote_base_str = remote_base_str.replace("\\", "/");
        let remote_base = Path::new(&remote_base_str);

        match upload_recursive(&conn.sftp, local_path, remote_base) {
            Ok(_) => return_ok(),
            Err(e) => return_err(e),
        }
    } else {
        return_err("Not connected")
    }
}

// 递归删除【✅】
fn sftp_rm_rf(sftp: &ssh2::Sftp, path: &Path) -> Result<(), ssh2::Error> {
    let stat = sftp.stat(path)?;
    if stat.is_file() {
        return sftp.unlink(path);
    }
    if stat.is_dir() {
        let entries = sftp.readdir(path)?;
        
        for (child_path, child_stat) in entries {
            let file_name = child_path.file_name().and_then(|n| n.to_str()).unwrap_or("");
            if file_name == "." || file_name == ".." {
                continue;
            }

            if child_stat.is_dir() {
                sftp_rm_rf(sftp, &child_path)?;
            } else {
                sftp.unlink(&child_path)?;
            }
        }
        return sftp.rmdir(path);
    }
    sftp.unlink(path)
}

// SFTP 删除【✅】
#[no_mangle]
pub extern "C" fn SftpDelete(path: *const c_char) -> *mut c_char {
    let path_str = c_str_to_string(path);
    let global = GLOBAL_SFTP.lock().unwrap();

    if let Some(conn) = &*global {
        let p = Path::new(&path_str);
        
        // 调用递归删除函数
        if let Err(e) = sftp_rm_rf(&conn.sftp, p) {
            return return_err(format!("Delete failed: {}", e));
        }

        return_ok()
    } else {
        return_err("Not connected")
    }
}

// SFTP 重命名【✅】
#[no_mangle]
pub extern "C" fn SftpRename(path: *const c_char, new_name: *const c_char) -> *mut c_char {
    let old_p = c_str_to_string(path);
    let new_p = c_str_to_string(new_name);
    if new_p.contains('/') || new_p.contains('\\') {
        return return_err("Invalid new name: cannot contain '/' or '\\'");
    }
    let old_path = Path::new(&old_p);
    let parent = old_path.parent().unwrap_or(Path::new("."));
    let new_path = parent.join(&new_p);
    let global = GLOBAL_SFTP.lock().unwrap();
    if let Some(conn) = &*global {
        match conn.sftp.rename(old_path, &new_path, None) {
            Ok(_) => return_ok(),
            Err(e) => return_err(format!("Rename failed: {}", e)),
        }
    } else {
        return_err("Not connected")
    }
}

// 断开连接【✅】
#[no_mangle]
pub extern "C" fn Disconnect() -> *mut c_char {
    let mut global = GLOBAL_SFTP.lock().unwrap();
    *global = None; // Drop the connection
    return_ok()
}

#[no_mangle]
pub extern "C" fn FreeString(ptr: *mut c_char) {
    if !ptr.is_null() {
        unsafe {
            let _ = CString::from_raw(ptr);
        }
    }
}