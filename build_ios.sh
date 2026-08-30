#!/bin/bash
set -e

DEVICE_SDK=$(xcrun --sdk iphoneos --show-sdk-path)
SIM_SDK=$(xcrun --sdk iphonesimulator --show-sdk-path)

CGO_ENABLED=1 GOOS=ios GOARCH=arm64 \
  CC="$(xcrun --sdk iphoneos --find clang)" \
  CGO_CFLAGS="-isysroot $DEVICE_SDK -arch arm64 -mios-version-min=13.0" \
  go build -buildmode=c-archive -o ./tmp/libcore-device.a ./main.go

CGO_ENABLED=1 GOOS=ios GOARCH=arm64 \
  CC="$(xcrun --sdk iphonesimulator --find clang)" \
  CGO_CFLAGS="-isysroot $SIM_SDK -arch arm64 -target arm64-apple-ios13.0-simulator" \
  go build -buildmode=c-archive -o ./tmp/libcore-sim.a ./main.go

cp ./tmp/libcore-device.h ./tmp/libcore.h

xcodebuild -create-xcframework \
  -library ./tmp/libcore-device.a -headers ./tmp \
  -library ./tmp/libcore-sim.a -headers ./tmp \
  -output ./build/ios/core.xcframework

echo "✅ Done! build/libcore.xcframework"