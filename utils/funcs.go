package utils

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

var globalSSHClient *ssh.Client
var globalSFTPClient *sftp.Client
var lock sync.Mutex
var sshConfig *ssh.ClientConfig
var sshAddr string

// 登录
func SshLogin(url, port, username, password string) string {
	lock.Lock()
	defer lock.Unlock()

	if globalSSHClient != nil {
		if sshAlive(globalSSHClient) {
			return "NOTE: Connected"
		}
		// 自动重连
		globalSSHClient.Close()
		globalSSHClient = nil
		globalSFTPClient = nil
	}

	sshConfig = &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}

	sshAddr = fmt.Sprintf("%s:%s", url, port)

	client, err := ssh.Dial("tcp", sshAddr, sshConfig)
	if err != nil {
		return fmt.Sprint("ERR: ", err.Error())
	}
	globalSSHClient = client

	sftpclient, err := sftp.NewClient(client)
	if err != nil {
		return fmt.Sprint("ERR: ", err.Error())
	}
	globalSFTPClient = sftpclient

	return "OK"
}

// 检查 SSH 是否活着
func sshAlive(client *ssh.Client) bool {
	sess, err := client.NewSession()
	if err != nil {
		return false
	}
	defer sess.Close()
	return sess.Run("true") == nil
}

// 自动重连
func reconnect() error {
	if sshConfig == nil {
		return fmt.Errorf("not logged in")
	}
	client, err := ssh.Dial("tcp", sshAddr, sshConfig)
	if err != nil {
		return err
	}
	globalSSHClient = client
	globalSFTPClient, err = sftp.NewClient(client)
	return err
}

// 下载某个文件到本地
func SftpDownload(path string, local string) string {
	lock.Lock()
	defer lock.Unlock()

	if globalSFTPClient == nil || !sshAlive(globalSSHClient) {
		if err := reconnect(); err != nil {
			return fmt.Sprint("ERR: ", err.Error())
		}
	}

	remoteFile, err := globalSFTPClient.Open(path)
	if err != nil {
		return fmt.Sprint("ERR: ", err.Error())
	}
	defer remoteFile.Close()
	localFile, err := os.Create(filepath.Join(local, filepath.Base(path)))
	if err != nil {
		return fmt.Sprint("ERR: ", err.Error())
	}
	defer localFile.Close()
	_, err = io.Copy(localFile, remoteFile)
	if err != nil {
		return fmt.Sprint("ERR: ", err.Error())
	}
	return "OK"
}

// 重命名文件
func SftpRename(oldPath string, newName string) string {
	lock.Lock()
	defer lock.Unlock()

	if globalSFTPClient == nil || !sshAlive(globalSSHClient) {
		if err := reconnect(); err != nil {
			return fmt.Sprint("ERR: ", err.Error())
		}
	}

	_, err := globalSFTPClient.Stat(oldPath)
	if err != nil {
		return fmt.Sprint("ERR: ", err.Error())
	}

	dir := path.Dir(oldPath)
	newPath := path.Join(dir, newName)

	_, err = globalSFTPClient.Stat(newPath)
	if err == nil {
		return "ERR: exist path"
	}

	err = globalSFTPClient.Rename(oldPath, newPath)
	if err != nil {
		return fmt.Sprint("ERR: ", err.Error())
	}

	return "OK"
}

// 删除文件
func SftpDelete(path string) string {
	lock.Lock()
	defer lock.Unlock()

	if globalSFTPClient == nil || !sshAlive(globalSSHClient) {
		if err := reconnect(); err != nil {
			return fmt.Sprint("ERR: ", err.Error())
		}
	}

	_, err := globalSFTPClient.Stat(path)
	if err != nil {
		return fmt.Sprint("ERR: ", err.Error())
	}

	fileInfo, err := globalSFTPClient.Stat(path)
	if err != nil {
		return fmt.Sprint("ERR: ", err.Error())
	}

	if fileInfo.IsDir() {
		err = globalSFTPClient.RemoveDirectory(path)
		if err != nil {
			return fmt.Sprint("ERR: ", err.Error())
		}
	} else {
		err = globalSFTPClient.Remove(path)
		if err != nil {
			return fmt.Sprint("ERR: ", err.Error())
		}
	}

	return "OK"
}

// 获取某个路径下所有的文件/目录
func SftpGetList(path string) string {
	lock.Lock()
	defer lock.Unlock()

	if globalSFTPClient == nil || !sshAlive(globalSSHClient) {
		if err := reconnect(); err != nil {
			return fmt.Sprint("ERR: ", err.Error())
		}
	}

	files, err := globalSFTPClient.ReadDir(path)
	if err != nil {
		return fmt.Sprint("ERR: ", err.Error())
	}

	type FileInfo struct {
		Type string `json:"type"`
		Name string `json:"name"`
		Size int64  `json:"size,omitempty"`
	}

	list := make([]FileInfo, 0)

	for _, file := range files {
		info := FileInfo{Name: file.Name()}
		if file.IsDir() {
			info.Type = "dir"
		} else {
			info.Type = "file"
			info.Size = file.Size()
		}
		list = append(list, info)
	}

	data, _ := json.Marshal(list)
	return string(data)
}

// 上传目录
func uploadDirectory(client *sftp.Client, localDir, remoteDir string) error {
	entries, err := os.ReadDir(localDir)
	if err != nil {
		return fmt.Errorf("%s", err.Error())
	}

	// 创建远程目录（如果不存在）
	client.MkdirAll(remoteDir)

	for _, entry := range entries {
		localPath := filepath.Join(localDir, entry.Name())
		remotePath := path.Join(remoteDir, entry.Name())

		if entry.IsDir() {
			err = uploadDirectory(client, localPath, remotePath)
			if err != nil {
				return err
			}
		} else {
			err = uploadFile(client, localPath, remotePath)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// 上传文件
func uploadFile(client *sftp.Client, localFile, remotePath string) error {
	srcFile, err := os.Open(localFile)
	if err != nil {
		fmt.Println("1")
		return fmt.Errorf("%s", err.Error())
	}
	defer srcFile.Close()

	dstFile, err := client.Create(remotePath)
	if err != nil {
		fmt.Println("2")
		return fmt.Errorf("%s", err.Error())
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		fmt.Println("3")
		return fmt.Errorf("%s", err.Error())
	}

	return nil
}

// 将本地文件上传到服务器
func SftpUpload(remotePath string, local string) string {
	lock.Lock()
	defer lock.Unlock()

	if globalSFTPClient == nil || !sshAlive(globalSSHClient) {
		if err := reconnect(); err != nil {
			return fmt.Sprint("ERR: ", err.Error())
		}
	}

	info, err := os.Stat(local)
	if err != nil {
		return fmt.Sprint("ERR: ", err.Error())
	}

	if info.IsDir() {
		err = uploadDirectory(globalSFTPClient, local, remotePath)
	} else {
		err = uploadFile(globalSFTPClient, local, remotePath)
	}

	if err != nil {
		return fmt.Sprint("ERR: ", err.Error())
	}
	return "OK"
}

// 断开连接
func Disconnect() string {
	if sshConfig == nil {
		return "ERR: Not logged in"
	}
	if globalSSHClient != nil {
		globalSSHClient.Close()
		globalSFTPClient.Close()
		globalSSHClient = nil
		globalSFTPClient = nil
	}
	return "OK"

}
