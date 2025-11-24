package utils

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
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

func SftpDelete(path string) string {
	lock.Lock()
	defer lock.Unlock()

	if globalSFTPClient == nil || !sshAlive(globalSSHClient) {
		if err := reconnect(); err != nil {
			return fmt.Sprint("ERR: ", err.Error())
		}
	}

	// 检查文件或目录是否存在
	_, err := globalSFTPClient.Stat(path)
	if err != nil {
		return fmt.Sprint("ERR: ", err.Error())
	}

	// 判断是文件还是目录
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

	var list []FileInfo

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
