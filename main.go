package main

import "C"
import "server_transfer_core/utils"

//export SSHLogin
func SSHLogin(url *C.char, port *C.char, username *C.char, password *C.char) *C.char {
	return C.CString(utils.SshLogin(C.GoString(url), C.GoString(port), C.GoString(username), C.GoString(password)))
}

//export SftpList
func SftpList(path *C.char) *C.char {
	return C.CString(utils.SftpGetList(C.GoString(path)))
}

//export Disconnect
func Disconnect() *C.char {
	return C.CString(utils.Disconnect())
}

func main() {}
