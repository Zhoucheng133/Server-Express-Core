# Server Express (Core)

![License](https://img.shields.io/badge/License-MIT-dark_green)

这是[Server Express](https://github.com/Zhoucheng133/Server-Express)的一部分，你也可以单独使用

This is part of [Server Express](https://github.com/Zhoucheng133/Server-Express), you can also use it alone

## 如果你想要自行打包成动态库

使用下面的命令来生成动态库

Use the following command to generate a dynamic library

```bash
# 对于Windows系统
go build -o build/core.dll -buildmode=c-shared .
# 对于macOS系统
go build -o build/core.dylib -buildmode=c-shared .
```