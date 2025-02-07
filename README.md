# Jpg-EXIF-Watermarker
批量读取jpg的 EXIF 信息、 GPS 位置，添加水印到照片中。
## 功能特点

- **批量处理**：支持批量处理指定目录下的 `.jpg` 文件。
- **EXIF 信息读取**：读取图片的 EXIF 信息，包括拍摄时间、GPS 位置等。
- **水印添加**：根据配置添加水印，支持自定义水印字体、颜色、位置等。
- **地址信息获取**：通过高德地图 API 根据 GPS 位置获取地址信息。
- **日志记录**：记录处理过程中的日志信息，方便排查问题。
- **多线程处理**：支持配置最大并发数，提高处理效率。

## 配置文件

配置文件为 `config.json`，默认配置如下：

```json
{
    "outputFolder": "已处理",
    "noExifFolder": "无EXIF信息",
    "jpegQuality": 70,
    "amapAPIKey": "",
    "maxConcurrency": 5,
    "fontPath": "C:/Windows/Fonts/msyh.ttc",
    "watermarkSettings": {
        "fontSize": 0.02,
        "widthPadding": 0.02,
        "heightPadding": 0.01,
        "color": {
            "r": 255,
            "g": 165,
            "b": 0,
            "a": 255
        }
    }
}
```
* `outputFolder`：处理后的图片存放目录。
* `noExifFolder`：无 EXIF 信息的图片存放目录。
* `jpegQuality`：保存图片的 JPEG 品质。
* `amapAPIKey`：高德地图 API 的 Key，用于获取地址信息, 可在这里申请[高德控制台](https://console.amap.com/dev/key/app)。
* `maxConcurrency`：最大并发数。
* `fontPath`：水印字体文件路径。
* `watermarkSettings`：水印相关设置，包括字体大小、边距、颜色等。
## 使用方法

### 安装依赖：

使用以下命令安装依赖：

```
go get -u github.com/disintegration/imaging
go get -u github.com/golang/freetype
go get -u github.com/rwcarlsen/goexif/exif
```

### 配置文件：

将默认配置文件内容保存为 `config.json`，并根据需要修改配置项。

### 运行程序：

将需要处理的 `.jpg` 文件放在程序所在目录下，运行程序：

```
go run main.go
```

也可以下载 `jpg-watermark-cli.exe` 运行。

处理后的图片会存放在配置文件中指定的 `outputFolder` 目录，无 EXIF 信息的图片会存放在 `noExifFolder` 目录。

## 注意事项

* 确保高德地图 API 的 Key 是有效的，否则无法获取地址信息。
* 水印字体文件路径需要正确，否则可能无法正常添加水印。
* 程序会根据图片的 EXIF 信息进行处理，如果图片没有 EXIF 信息，会被复制到 `noExifFolder` 目录。

## 项目结构

* `config.json`：配置文件。
* `process.log`：日志文件，记录处理过程中的信息。
