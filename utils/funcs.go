package utils

import (
	"encoding/json"
	"fmt"
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

	return "Ok"
}

// 检查 SSH 是否活着
func sshAlive(client *ssh.Client) bool {
	_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
	return err == nil
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
	return "Ok"

}
