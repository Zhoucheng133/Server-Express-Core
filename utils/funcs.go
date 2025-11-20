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

func SshLogin(url string, port string, username string, password string) string {
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

	defer client.Close()

	session, _ := client.NewSession()
	defer session.Close()

	out, _ := session.CombinedOutput("hostname")

	return string(out)

}

func SftpGetList(url string, port string, username string, password string, path string) string {
	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	sshClient, err := ssh.Dial("tcp", fmt.Sprint(url, ":", port), config)

	if err != nil {
		return fmt.Sprint("ERR: ", err.Error())
	}

	defer sshClient.Close()

	client, err := sftp.NewClient(sshClient)
	if err != nil {
		return fmt.Sprint("ERR: ", err.Error())
	}
	defer client.Close()

	files, err := client.ReadDir(path)
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
