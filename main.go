package main

import "C"
import "server_express_core/utils"

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

//export SftpDownload
func SftpDownload(path *C.char, local *C.char) *C.char {
	return C.CString(utils.SftpDownload(C.GoString(path), C.GoString(local)))
}

//export SftpDelete
func SftpDelete(path *C.char) *C.char {
	return C.CString(utils.SftpDelete(C.GoString(path)))
}

//export SftpRename
func SftpRename(path *C.char, newName *C.char) *C.char {
	return C.CString(utils.SftpRename(C.GoString(path), C.GoString(newName)))
}

func main() {}
