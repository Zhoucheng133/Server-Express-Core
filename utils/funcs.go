package utils

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type FileInfo struct {
	Type string `json:"type"`           // "dir" 或 "file"
	Name string `json:"name"`           // 文件/目录名
	Size int64  `json:"size,omitempty"` // 文件大小，目录不显示
}

var globalSSHClient *ssh.Client
var globalSFTPClient *sftp.Client

func SshLogin(url string, port string, username string, password string) string {

	if globalSSHClient != nil {
		return "NOTE: Connected"
	}

	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("tcp", fmt.Sprint(url, ":", port), config)

	if err != nil {
		return fmt.Sprint("ERR: ", err.Error())
	}

	globalSSHClient = client

	// defer client.Close()

	// session, _ := client.NewSession()
	// defer session.Close()

	sftpclient, err := sftp.NewClient(globalSSHClient)
	if err != nil {
		return fmt.Sprint("ERR: ", err.Error())
	}
	globalSFTPClient = sftpclient

	// out, _ := session.CombinedOutput("hostname")

	return "ok"

}

func SftpGetList(path string) string {

	// client, err := sftp.NewClient(globalSSHClient)
	// if err != nil {
	// 	return fmt.Sprint("ERR: ", err.Error())
	// }
	// defer client.Close()

	if globalSSHClient == nil {
		return "ERR: Not connected"
	}

	files, err := globalSFTPClient.ReadDir(path)
	if err != nil {
		return fmt.Sprint("ERR: ", err.Error())
	}

	var list []FileInfo

	for _, file := range files {
		info := FileInfo{
			Name: file.Name(),
		}
		if file.IsDir() {
			info.Type = "dir"
		} else {
			info.Type = "file"
			info.Size = file.Size()
		}
		list = append(list, info)
	}

	jsonData, err := json.Marshal(list)
	if err != nil {
		log.Fatal(err)
	}

	return string(jsonData)

}
