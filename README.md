# Server Express (Core)

![License](https://img.shields.io/badge/License-MIT-dark_green)

这是[Server Express](https://github.com/Zhoucheng133/Server-Express)的一部分，基于Go开发  
This is part of [Server Express](https://github.com/Zhoucheng133/Server-Express). Written in Go

## Build | 构建

### macOS

```bash
go build -buildmode=c-shared -ldflags="-s -w" -o build/macos/core.dylib
```

### Windows

```bash
go build -buildmode=c-shared -ldflags="-s -w" -o build/windows/core.dll
```

### iOS

```bash
chmod +x build_ios.sh
./build_ios.sh
```

### Android

#### Windows (Simulator)

```bash
$env:NDK_PATH=/path/to/your/android-ndk

$env:CC="$env:NDK_PATH\toolchains\llvm\prebuilt\windows-x86_64\bin\x86_64-linux-android30-clang.cmd"

$env:CGO_ENABLED="1"
$env:GOOS="android"
$env:GOARCH="amd64"
$env:CGO_ASFLAGS="-target x86_64-linux-android"

go build -buildmode=c-shared -o build/android/libcore.so
```

#### Windows (arm64)

```bash
$env:NDK_PATH=/path/to/your/android-ndk

$env:CC="$env:NDK_PATH\toolchains\llvm\prebuilt\windows-x86_64\bin\aarch64-linux-android30-clang.cmd"

$env:CGO_ENABLED="1"
$env:GOOS="android"
$env:GOARCH="arm64"

go build -buildmode=c-shared -o build/android/libcore.so
```

#### macOS (arm64)
```bash
export NDK_PATH=/path/to/your/android-ndk
export CC_PATH=$NDK_PATH/toolchains/llvm/prebuilt/darwin-x86_64/bin/aarch64-linux-android30-clang

CGO_ENABLED=1 \
GOOS=android \
GOARCH=arm64 \
CC=$CC_PATH \
go build -buildmode=c-shared -o build/android/libcore.so
```