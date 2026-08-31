package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type SftpConnection struct {
	client  *ssh.Client
	sftpCli *sftp.Client
}

type TransferProgress struct {
	Active                bool   `json:"active"`
	TotalBytes            uint64 `json:"total_bytes"`
	TransferredBytes      uint64 `json:"transferred_bytes"`
	CurrentFile           string `json:"current_file"`
	CurrentFileBytes      uint64 `json:"current_file_bytes"`
	CurrentFileTotalBytes uint64 `json:"current_file_total_bytes"`
}

type FileInfo struct {
	FileType string  `json:"type"`
	Name     string  `json:"name"`
	Size     *uint64 `json:"size,omitempty"`
}

var (
	globalSftpMutex sync.Mutex
	globalSftpConn  *SftpConnection

	progressMutex    sync.Mutex
	transferProgress = TransferProgress{}

	cancelFlag atomic.Bool
)

func returnString(s string) *C.char {
	return C.CString(s)
}

func returnOk() *C.char {
	return returnString("OK")
}

func returnErr(format string, args ...interface{}) *C.char {
	msg := fmt.Sprintf(format, args...)
	return returnString("ERR: " + msg)
}

func startTransfer(totalBytes uint64) {
	progressMutex.Lock()
	defer progressMutex.Unlock()
	transferProgress = TransferProgress{
		Active:     true,
		TotalBytes: totalBytes,
	}
}

func finishTransfer() {
	progressMutex.Lock()
	defer progressMutex.Unlock()
	transferProgress.Active = false
}

func startFile(path string, totalBytes uint64) {
	progressMutex.Lock()
	defer progressMutex.Unlock()
	transferProgress.CurrentFile = path
	transferProgress.CurrentFileTotalBytes = totalBytes
	transferProgress.CurrentFileBytes = 0
}

func addTransferred(bytes uint64) {
	progressMutex.Lock()
	defer progressMutex.Unlock()
	transferProgress.TransferredBytes += bytes
	transferProgress.CurrentFileBytes += bytes
}

func isCancelled() bool {
	return cancelFlag.Load()
}

func resetCancel() {
	cancelFlag.Store(false)
}

//export SftpCancel
func SftpCancel() *C.char {
	cancelFlag.Store(true)
	return returnOk()
}

//export SftpTransferProgress
func SftpTransferProgress() *C.char {
	progressMutex.Lock()
	p := transferProgress
	progressMutex.Unlock()

	data, err := json.Marshal(p)
	if err != nil {
		return returnErr("Progress serialization failed: %v", err)
	}
	return returnString(string(data))
}

//export SSHLogin
func SSHLogin(url, port, username, password *C.char) *C.char {
	addr := net.JoinHostPort(C.GoString(url), C.GoString(port))
	user := C.GoString(username)
	pass := C.GoString(password)

	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(pass),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}

	conn, err := net.DialTimeout("tcp", addr, 15*time.Second)
	if err != nil {
		return returnErr("TCP connection failed: %v", err)
	}

	c, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		conn.Close()
		return returnErr("Handshake/Authentication failed: %v", err)
	}
	client := ssh.NewClient(c, chans, reqs)

	sftpCli, err := sftp.NewClient(client)
	if err != nil {
		client.Close()
		return returnErr("SFTP initialization failed: %v", err)
	}

	globalSftpMutex.Lock()
	defer globalSftpMutex.Unlock()
	if globalSftpConn != nil {
		if globalSftpConn.sftpCli != nil {
			globalSftpConn.sftpCli.Close()
		}
		if globalSftpConn.client != nil {
			globalSftpConn.client.Close()
		}
	}
	globalSftpConn = &SftpConnection{
		client:  client,
		sftpCli: sftpCli,
	}

	return returnOk()
}

//export SftpList
func SftpList(path *C.char) *C.char {
	pStr := normalizePath(C.GoString(path))

	globalSftpMutex.Lock()
	conn := globalSftpConn
	globalSftpMutex.Unlock()

	if conn == nil || conn.sftpCli == nil {
		return returnErr("Not connected")
	}

	entries, err := conn.sftpCli.ReadDir(pStr)
	if err != nil {
		return returnErr("List failed: %v", err)
	}

	fileInfos := make([]FileInfo, 0)
	for _, entry := range entries {
		name := entry.Name()
		if name == "." || name == ".." {
			continue
		}
		fType := "file"
		var size *uint64 = nil
		if entry.IsDir() {
			fType = "dir"
		} else {
			s := uint64(entry.Size())
			size = &s
		}
		fileInfos = append(fileInfos, FileInfo{
			FileType: fType,
			Name:     name,
			Size:     size,
		})
	}

	data, err := json.Marshal(fileInfos)
	if err != nil {
		return returnErr("JSON serialization failed: %v", err)
	}
	return returnString(string(data))
}

func remoteSizeRecursive(sftpCli *sftp.Client, path string) (uint64, error) {
	info, err := sftpCli.Stat(path)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return uint64(info.Size()), nil
	}

	entries, err := sftpCli.ReadDir(path)
	if err != nil {
		return 0, err
	}

	var total uint64 = 0
	for _, entry := range entries {
		name := entry.Name()
		if name == "." || name == ".." {
			continue
		}
		childPath := filepath.ToSlash(filepath.Join(path, name))
		sz, err := remoteSizeRecursive(sftpCli, childPath)
		if err != nil {
			return 0, err
		}
		total += sz
	}
	return total, nil
}

func downloadRecursive(sftpCli *sftp.Client, remotePath, localPath string) error {
	if isCancelled() {
		return fmt.Errorf("Cancelled")
	}

	info, err := sftpCli.Stat(remotePath)
	if err != nil {
		return err
	}

	if info.IsDir() {
		if err := os.MkdirAll(localPath, 0755); err != nil {
			return err
		}
		entries, err := sftpCli.ReadDir(remotePath)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			name := entry.Name()
			if name == "." || name == ".." {
				continue
			}
			childRemote := filepath.ToSlash(filepath.Join(remotePath, name))
			childLocal := filepath.Join(localPath, name)
			if err := downloadRecursive(sftpCli, childRemote, childLocal); err != nil {
				return err
			}
		}
	} else {
		if parent := filepath.Dir(localPath); parent != "" {
			if err := os.MkdirAll(parent, 0755); err != nil {
				return err
			}
		}

		fileSize := uint64(info.Size())
		startFile(remotePath, fileSize)

		rFile, err := sftpCli.Open(remotePath)
		if err != nil {
			return err
		}
		defer rFile.Close()

		lFile, err := os.Create(localPath)
		if err != nil {
			return err
		}
		defer lFile.Close()

		buf := make([]byte, 512*1024)
		for {
			if isCancelled() {
				return fmt.Errorf("Cancelled")
			}
			n, err := rFile.Read(buf)
			if n > 0 {
				if _, werr := lFile.Write(buf[:n]); werr != nil {
					return werr
				}
				addTransferred(uint64(n))
			}
			if err != nil {
				if err == io.EOF {
					break
				}
				return err
			}
		}
	}
	return nil
}

//export SftpDownload
func SftpDownload(path, local *C.char) *C.char {
	remotePathStr := normalizePath(C.GoString(path))
	localBaseStr := C.GoString(local)

	globalSftpMutex.Lock()
	conn := globalSftpConn
	globalSftpMutex.Unlock()

	if conn == nil || conn.sftpCli == nil {
		return returnErr("Not connected")
	}

	baseName := filepath.Base(remotePathStr)
	if baseName == "" || baseName == "." || baseName == "/" {
		return returnErr("Invalid remote path")
	}
	targetLocal := filepath.Join(localBaseStr, baseName)

	total, err := remoteSizeRecursive(conn.sftpCli, remotePathStr)
	if err != nil {
		return returnErr("%v", err)
	}

	reset_cancel_func := func() { resetCancel() }
	reset_cancel_func()

	startTransfer(total)
	dlErr := downloadRecursive(conn.sftpCli, remotePathStr, targetLocal)
	finishTransfer()

	if dlErr != nil {
		if dlErr.Error() == "Cancelled" {
			return returnOk()
		}
		return returnErr("%v", dlErr)
	}
	return returnOk()
}

func normalizePath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	cleaned := filepath.Clean(p)
	return filepath.ToSlash(cleaned)
}

func ensureRemoteDir(sftpCli *sftp.Client, remoteDir string) error {
	remoteDir = normalizePath(remoteDir)
	parts := strings.Split(remoteDir, "/")
	cur := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		cur += "/" + part
		_ = sftpCli.Mkdir(cur)
	}
	return nil
}

func remoteJoin(base string, name string) string {
	base = normalizePath(base)
	return strings.TrimRight(base, "/") + "/" + name
}

func localSizeRecursive(path string) (uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return uint64(info.Size()), nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return 0, err
	}

	var total uint64 = 0
	for _, entry := range entries {
		p := filepath.Join(path, entry.Name())
		sz, err := localSizeRecursive(p)
		if err != nil {
			return 0, err
		}
		total += sz
	}
	return total, nil
}

func uploadRecursive(sftpCli *sftp.Client, localPath, remotePath string) error {
	if isCancelled() {
		return fmt.Errorf("Cancelled")
	}

	info, err := os.Stat(localPath)
	if err != nil {
		return err
	}

	if info.IsDir() {
		if err := ensureRemoteDir(sftpCli, remotePath); err != nil {
			return err
		}
		entries, err := os.ReadDir(localPath)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			childLocal := filepath.Join(localPath, entry.Name())
			childRemote := remoteJoin(remotePath, entry.Name())
			if err := uploadRecursive(sftpCli, childLocal, childRemote); err != nil {
				return err
			}
		}
	} else {
		parent := filepath.ToSlash(filepath.Dir(remotePath))
		if parent != "" && parent != "." {
			_ = ensureRemoteDir(sftpCli, parent)
		}

		fileSize := uint64(info.Size())
		startFile(localPath, fileSize)

		lFile, err := os.Open(localPath)
		if err != nil {
			return err
		}
		defer lFile.Close()

		rFile, err := sftpCli.Create(remotePath)
		if err != nil {
			return err
		}
		defer rFile.Close()

		buf := make([]byte, 512*1024)
		for {
			if isCancelled() {
				return fmt.Errorf("Cancelled")
			}
			n, err := lFile.Read(buf)
			if n > 0 {
				if _, werr := rFile.Write(buf[:n]); werr != nil {
					return werr
				}
				addTransferred(uint64(n))
			}
			if err != nil {
				if err == io.EOF {
					break
				}
				return err
			}
		}
	}
	return nil
}

//export SftpUpload
func SftpUpload(path, local *C.char) *C.char {
	remoteBaseStr := normalizePath(C.GoString(path))
	localPathStr := C.GoString(local)

	globalSftpMutex.Lock()
	conn := globalSftpConn
	globalSftpMutex.Unlock()

	if conn == nil || conn.sftpCli == nil {
		return returnErr("Not connected")
	}

	remoteBaseStr = strings.ReplaceAll(remoteBaseStr, "\\", "/")

	total, err := localSizeRecursive(localPathStr)
	if err != nil {
		return returnErr("%v", err)
	}

	resetCancel()
	startTransfer(total)
	upErr := uploadRecursive(conn.sftpCli, localPathStr, remoteBaseStr)
	finishTransfer()

	if upErr != nil {
		if upErr.Error() == "Cancelled" {
			return returnOk()
		}
		return returnErr("%v", upErr)
	}
	return returnOk()
}

func sftpRmRf(sftpCli *sftp.Client, path string) error {
	info, err := sftpCli.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return sftpCli.Remove(path)
	}

	entries, err := sftpCli.ReadDir(path)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		name := entry.Name()
		if name == "." || name == ".." {
			continue
		}
		childPath := filepath.ToSlash(filepath.Join(path, name))
		if err := sftpRmRf(sftpCli, childPath); err != nil {
			return err
		}
	}
	return sftpCli.RemoveDirectory(path)
}

//export SftpDelete
func SftpDelete(path *C.char) *C.char {
	pathStr := normalizePath(C.GoString(path))

	globalSftpMutex.Lock()
	conn := globalSftpConn
	globalSftpMutex.Unlock()

	if conn == nil || conn.sftpCli == nil {
		return returnErr("Not connected")
	}

	if err := sftpRmRf(conn.sftpCli, pathStr); err != nil {
		return returnErr("Delete failed: %v", err)
	}
	return returnOk()
}

//export SftpRename
func SftpRename(path, newName *C.char) *C.char {
	oldP := normalizePath(C.GoString(path))
	newP := C.GoString(newName)

	if strings.Contains(newP, "/") || strings.Contains(newP, "\\") {
		return returnErr("Invalid new name: cannot contain '/' or '\\'")
	}

	parent := filepath.ToSlash(filepath.Dir(oldP))
	var newPath string
	if parent == "" || parent == "." {
		newPath = newP
	} else {
		newPath = parent + "/" + newP
	}

	globalSftpMutex.Lock()
	conn := globalSftpConn
	globalSftpMutex.Unlock()

	if conn == nil || conn.sftpCli == nil {
		return returnErr("Not connected")
	}

	if err := conn.sftpCli.Rename(oldP, newPath); err != nil {
		return returnErr("Rename failed: %v", err)
	}
	return returnOk()
}

//export SftpMkdir
func SftpMkdir(path, name *C.char) *C.char {
	pStr := normalizePath(C.GoString(path))
	nStr := C.GoString(name)

	var fullPath string
	if strings.HasSuffix(pStr, "/") {
		fullPath = pStr + nStr
	} else {
		fullPath = pStr + "/" + nStr
	}
	fullPath = normalizePath(fullPath)

	globalSftpMutex.Lock()
	conn := globalSftpConn
	globalSftpMutex.Unlock()

	if conn == nil || conn.sftpCli == nil {
		return returnErr("Not connected")
	}

	if err := conn.sftpCli.Mkdir(fullPath); err != nil {
		return returnErr("Mkdir failed: %v", err)
	}
	return returnOk()
}

func sftpGetTotalSize(sftpCli *sftp.Client, path string) (uint64, error) {
	info, err := sftpCli.Stat(path)
	if err != nil {
		return 0, err
	}
	if info.IsDir() {
		entries, err := sftpCli.ReadDir(path)
		if err != nil {
			return 0, err
		}
		var total uint64 = 0
		for _, entry := range entries {
			name := entry.Name()
			if name == "." || name == ".." {
				continue
			}
			childPath := filepath.ToSlash(filepath.Join(path, name))
			sz, err := sftpGetTotalSize(sftpCli, childPath)
			if err != nil {
				return 0, err
			}
			total += sz
		}
		return total, nil
	}
	return uint64(info.Size()), nil
}

func sftpCopyRecursive(sftpCli *sftp.Client, src, dest string) error {
	if isCancelled() {
		return fmt.Errorf("Cancelled")
	}

	info, err := sftpCli.Stat(src)
	if err != nil {
		return err
	}

	if info.IsDir() {
		_ = sftpCli.Mkdir(dest)
		entries, err := sftpCli.ReadDir(src)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if isCancelled() {
				return fmt.Errorf("Cancelled")
			}
			name := entry.Name()
			if name == "." || name == ".." {
				continue
			}
			childSrc := filepath.ToSlash(filepath.Join(src, name))
			childDest := filepath.ToSlash(filepath.Join(dest, name))
			if err := sftpCopyRecursive(sftpCli, childSrc, childDest); err != nil {
				return err
			}
		}
	} else {
		if parent := filepath.ToSlash(filepath.Dir(dest)); parent != "" && parent != "." {
			_ = ensureRemoteDir(sftpCli, parent)
		}

		fileSize := uint64(info.Size())
		startFile(src, fileSize)

		srcFile, err := sftpCli.Open(src)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		destFile, err := sftpCli.Create(dest)
		if err != nil {
			return err
		}
		defer destFile.Close()

		buf := make([]byte, 512*1024)
		for {
			if isCancelled() {
				return fmt.Errorf("Cancelled")
			}
			n, err := srcFile.Read(buf)
			if n > 0 {
				if _, werr := destFile.Write(buf[:n]); werr != nil {
					return werr
				}
				addTransferred(uint64(n))
			}
			if err != nil {
				if err == io.EOF {
					break
				}
				return err
			}
		}
	}
	return nil
}

//export SftpCopy
func SftpCopy(path, dest, filesJson *C.char) *C.char {
	pathStr := normalizePath(C.GoString(path))
	destStr := normalizePath(C.GoString(dest))
	filesJsonStr := C.GoString(filesJson)

	var files []string
	if err := json.Unmarshal([]byte(filesJsonStr), &files); err != nil {
		return returnErr("Invalid files json: %v", err)
	}

	resetCancel()

	globalSftpMutex.Lock()
	conn := globalSftpConn
	globalSftpMutex.Unlock()

	if conn == nil || conn.sftpCli == nil {
		return returnErr("Not connected")
	}

	pathP := filepath.ToSlash(pathStr)
	destP := filepath.ToSlash(destStr)

	var totalBytes uint64 = 0
	for _, file := range files {
		filePath := filepath.ToSlash(filepath.Join(pathP, file))
		sz, err := sftpGetTotalSize(conn.sftpCli, filePath)
		if err != nil {
			return returnErr("File not found in path: %s", file)
		}
		totalBytes += sz
	}

	startTransfer(totalBytes)

	var copyErr error = nil
	for _, file := range files {
		if isCancelled() {
			copyErr = fmt.Errorf("Cancelled")
			break
		}
		srcPath := filepath.ToSlash(filepath.Join(pathP, file))
		destPath := filepath.ToSlash(filepath.Join(destP, file))
		if err := sftpCopyRecursive(conn.sftpCli, srcPath, destPath); err != nil {
			copyErr = fmt.Errorf("Copy failed for %s: %v", file, err)
			break
		}
	}

	finishTransfer()

	if copyErr != nil {
		if copyErr.Error() == "Cancelled" {
			return returnErr("Cancelled")
		}
		return returnErr("%v", copyErr)
	}

	if isCancelled() {
		return returnErr("Cancelled")
	}
	return returnOk()
}

//export SftpMove
func SftpMove(path, dest, filesJson *C.char) *C.char {
	pathStr := normalizePath(C.GoString(path))
	destStr := normalizePath(C.GoString(dest))
	filesJsonStr := C.GoString(filesJson)

	var files []string
	if err := json.Unmarshal([]byte(filesJsonStr), &files); err != nil {
		return returnErr("Invalid files json: %v", err)
	}

	resetCancel()

	globalSftpMutex.Lock()
	conn := globalSftpConn
	globalSftpMutex.Unlock()

	if conn == nil || conn.sftpCli == nil {
		return returnErr("Not connected")
	}

	pathP := filepath.ToSlash(pathStr)
	destP := filepath.ToSlash(destStr)

	var totalBytes uint64 = 0
	for _, file := range files {
		filePath := filepath.ToSlash(filepath.Join(pathP, file))
		sz, err := sftpGetTotalSize(conn.sftpCli, filePath)
		if err != nil {
			return returnErr("File not found in path: %s", file)
		}
		totalBytes += sz
	}

	startTransfer(totalBytes)

	var moveErr error = nil
	for _, file := range files {
		if isCancelled() {
			moveErr = fmt.Errorf("Cancelled")
			break
		}
		srcPath := filepath.ToSlash(filepath.Join(pathP, file))
		destPath := filepath.ToSlash(filepath.Join(destP, file))

		if err := conn.sftpCli.Rename(srcPath, destPath); err != nil {
			// Fallback to copy then remove
			if err := sftpCopyRecursive(conn.sftpCli, srcPath, destPath); err != nil {
				moveErr = fmt.Errorf("Move (copy part) failed for %s: %v", file, err)
				break
			}
			if err := sftpRmRf(conn.sftpCli, srcPath); err != nil {
				moveErr = fmt.Errorf("Move (remove old part) failed for %s: %v", file, err)
				break
			}
		} else {
			if sz, err := sftpGetTotalSize(conn.sftpCli, destPath); err == nil {
				addTransferred(sz)
			}
		}
	}

	finishTransfer()

	if moveErr != nil {
		if moveErr.Error() == "Cancelled" {
			return returnErr("Cancelled")
		}
		return returnErr("%v", moveErr)
	}

	if isCancelled() {
		return returnErr("Cancelled")
	}
	return returnOk()
}

//export Disconnect
func Disconnect() *C.char {
	globalSftpMutex.Lock()
	defer globalSftpMutex.Unlock()

	if globalSftpConn != nil {
		if globalSftpConn.sftpCli != nil {
			globalSftpConn.sftpCli.Close()
		}
		if globalSftpConn.client != nil {
			globalSftpConn.client.Close()
		}
		globalSftpConn = nil
	}
	return returnOk()
}

//export FreeString
func FreeString(ptr *C.char) {
	if ptr != nil {
		C.free(unsafe.Pointer(ptr))
	}
}

func main() {}
