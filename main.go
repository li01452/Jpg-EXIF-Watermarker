package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/disintegration/imaging"
	"github.com/golang/freetype"
	"github.com/rwcarlsen/goexif/exif"
)

// Config 结构体用于存储配置信息
type Config struct {
	OutputFolder      string `json:"outputFolder"`
	NoExifFolder      string `json:"noExifFolder"`
	JpegQuality       int    `json:"jpegQuality"`
	AmapAPIKey        string `json:"amapAPIKey"`
	MaxConcurrency    int    `json:"maxConcurrency"`
	FontPath          string `json:"fontPath"`
	WatermarkSettings struct {
		FontSize      float64 `json:"fontSize"`
		WidthPadding  float64 `json:"widthPadding"`
		HeightPadding float64 `json:"heightPadding"`
		Color         struct {
			R uint8 `json:"r"`
			G uint8 `json:"g"`
			B uint8 `json:"b"`
			A uint8 `json:"a"`
		} `json:"color"`
	} `json:"watermarkSettings"`
}

const configJSON = `{
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
}`

// AmapResponse 定义高德地图API的响应结构
type AmapResponse struct {
	Status    string `json:"status"`
	Regeocode struct {
		AddressComponent struct {
			Province string   `json:"province"`
			City     []string `json:"city"`
			District string   `json:"district"`
		} `json:"addressComponent"`
	} `json:"regeocode"`
}

var (
	config Config
	wg     sync.WaitGroup
	mu     sync.Mutex
)

// LoadConfig 加载配置文件
func LoadConfig() error {
	file, err := os.Open("config.json")
	if err != nil {
		return fmt.Errorf("打开配置文件失败: %v", err)

	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&config); err != nil {
		return fmt.Errorf("解析配置文件失败: %v", err)
	}
	return nil
}

func main() {
	fmt.Println("开始处理图片,若有问题请检查process.log")
	if err := LoadConfig(); err != nil {
		log.Fatalf("加载配置失败: %v", err)
		saveConfig(configJSON)
	}

	sem := make(chan struct{}, config.MaxConcurrency)
	processedFiles := make(map[string]bool)

	if err := initializeLogger(); err != nil {
		log.Fatalf("初始化日志失败: %v", err)
	}

	if err := createRequiredDirectories(); err != nil {
		log.Fatalf("创建目录失败: %v", err)
	}

	files, err := filepath.Glob("*.jpg")
	if err != nil {
		log.Fatalf("获取jpg文件失败: %v", err)
	}

	for _, file := range files {
		sem <- struct{}{}
		wg.Add(1)
		go func(filename string) {
			defer func() {
				<-sem
				wg.Done()
			}()
			if err := processImage(filename, processedFiles); err != nil {
				log.Printf("处理文件 %s 失败: %v", filename, err)
			}
		}(file)
	}

	wg.Wait()
	log.Println("所有文件处理完成")
}

func initializeLogger() error {
	logFile, err := os.OpenFile("process.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("创建日志文件失败: %v", err)
	}
	log.SetOutput(logFile)
	log.Println("日志初始化完成")
	return nil
}

func createRequiredDirectories() error {
	dirs := []string{config.OutputFolder, config.NoExifFolder}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			return fmt.Errorf("创建目录 %s 失败: %v", dir, err)
		}
		log.Printf("目录 %s 创建成功", dir)
	}
	return nil
}

func processImage(filename string, processedFiles map[string]bool) error {
	mu.Lock()
	if processedFiles[filename] {
		mu.Unlock()
		return nil
	}
	processedFiles[filename] = true
	mu.Unlock()

	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("打开文件失败: %v", err)
	}
	defer file.Close()

	x, err := exif.Decode(file)
	if err != nil {
		return copyToNoExifFolder(filename)
	}

	orientation, _ := x.Get(exif.Orientation)
	var orientationValue int
	if orientation != nil {
		orientationValue, _ = orientation.Int(0)
	}

	timeStr, err := x.DateTime()
	if err != nil || timeStr.IsZero() {
		return copyToNoExifFolder(filename)
	}

	addressChan := make(chan string, 1)

	go func() {
		lat, long, err := x.LatLong()
		if err != nil {
			addressChan <- ""
			return
		}
		address := getAddressFromGPS(lat, long)
		addressChan <- address
	}()

	address := <-addressChan

	return processImageWithWatermark(filename, timeStr, address, orientationValue)
}

func processImageWithWatermark(filename string, timeStr time.Time, address string, orientation int) error {
	fmt.Println("处理图片： " + filename)
	img, err := imaging.Open(filename)
	if err != nil {
		return fmt.Errorf("打开图片失败: %v", err)
	}

	img = rotateImage(img, orientation)

	watermarkText := fmt.Sprintf("%s\n%s", timeStr.Format("2006-01-02 15:04:05"), address)
	watermarkedImg := addWatermark(img, watermarkText)

	outputPath := filepath.Join(config.OutputFolder, timeStr.Format("20060102150405")+".jpg")
	return imaging.Save(watermarkedImg, outputPath, imaging.JPEGQuality(config.JpegQuality))
}

func rotateImage(img image.Image, orientation int) image.Image {
	switch orientation {
	case 3:
		return imaging.Rotate180(img)
	case 6:
		return imaging.Rotate270(img)
	case 8:
		return imaging.Rotate90(img)
	default:
		return img
	}
}

func addWatermark(img image.Image, text string) image.Image {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	// 使用长边计算字体大小
	maxSide := width
	if height > width {
		maxSide = height
	}
	fontSize := float64(maxSide) * config.WatermarkSettings.FontSize

	widthPadding := int(float64(width) * config.WatermarkSettings.WidthPadding)
	heightPadding := int(float64(height) * config.WatermarkSettings.HeightPadding)

	rgba := image.NewRGBA(bounds)
	draw.Draw(rgba, bounds, img, bounds.Min, draw.Src)

	fontBytes, err := os.ReadFile(config.FontPath)
	if err != nil {
		log.Printf("加载字体文件失败: %v", err)
		return rgba
	}

	font, err := freetype.ParseFont(fontBytes)
	if err != nil {
		log.Printf("解析字体失败: %v", err)
		return rgba
	}

	lines := strings.Split(text, "\n")
	lineHeight := int(fontSize * 1.2)
	maxWidth := 0
	for _, line := range lines {
		width := int(fontSize * float64(len(line)) * 0.6)
		if width > maxWidth {
			maxWidth = width
		}
	}

	x := bounds.Max.X - maxWidth - widthPadding
	y := bounds.Max.Y - (lineHeight * len(lines)) - heightPadding

	// 创建描边效果
	strokeOffsets := []struct{ dx, dy int }{
		{-2, -2}, {-2, 0}, {-2, 2},
		{0, -2}, {0, 2},
		{2, -2}, {2, 0}, {2, 2},
	}

	// 先绘制黑色描边
	c := freetype.NewContext()
	c.SetDPI(72)
	c.SetFont(font)
	c.SetFontSize(fontSize)
	c.SetClip(bounds)
	c.SetDst(rgba)
	c.SetSrc(image.NewUniform(color.RGBA{0, 0, 0, 255})) // 黑色描边

	for _, line := range lines {
		for _, offset := range strokeOffsets {
			pt := freetype.Pt(x+offset.dx, y+int(fontSize)+offset.dy)
			_, err := c.DrawString(line, pt)
			if err != nil {
				log.Printf("绘制描边文本失败: %v", err)
			}
		}
		y += lineHeight
	}

	// 重置y坐标
	y = bounds.Max.Y - (lineHeight * len(lines)) - heightPadding

	// 绘制阴影
	shadowOffsets := []struct{ dx, dy int }{
		{4, 4}, {3, 3}, {5, 5},
	}

	c.SetSrc(image.NewUniform(color.RGBA{0, 0, 0, 180})) // 半透明黑色阴影
	for _, line := range lines {
		for _, offset := range shadowOffsets {
			pt := freetype.Pt(x+offset.dx, y+int(fontSize)+offset.dy)
			_, err := c.DrawString(line, pt)
			if err != nil {
				log.Printf("绘制阴影文本失败: %v", err)
			}
		}
		y += lineHeight
	}

	// 重置y坐标
	y = bounds.Max.Y - (lineHeight * len(lines)) - heightPadding

	// 最后绘制主要文本
	c.SetSrc(image.NewUniform(color.RGBA{
		config.WatermarkSettings.Color.R,
		config.WatermarkSettings.Color.G,
		config.WatermarkSettings.Color.B,
		config.WatermarkSettings.Color.A,
	}))

	for _, line := range lines {
		pt := freetype.Pt(x, y+int(fontSize))
		if _, err := c.DrawString(line, pt); err != nil {
			log.Printf("绘制主要文本失败: %v", err)
		}
		y += lineHeight
	}

	return rgba
}

func copyToNoExifFolder(filename string) error {
	sourcePath := filename
	newPath := filepath.Join(config.NoExifFolder, filename)

	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("无法打开源文件 %s: %v", sourcePath, err)
	}
	defer sourceFile.Close()

	targetFile, err := os.Create(newPath)
	if err != nil {
		return fmt.Errorf("无法创建目标文件 %s: %v", newPath, err)
	}
	defer targetFile.Close()

	_, err = io.Copy(targetFile, sourceFile)
	if err != nil {
		return fmt.Errorf("复制文件内容失败: %v", err)
	}

	log.Printf("已复制文件: %s -> %s", sourcePath, newPath)
	return nil
}

func getAddressFromGPS(lat, long float64) string {
	url := fmt.Sprintf("https://restapi.amap.com/v3/geocode/regeo?output=JSON&location=%.6f,%.6f&key=%s&radius=10", long, lat, config.AmapAPIKey)

	resp, err := http.Get(url)
	if err != nil {
		log.Printf("高德API请求失败: %v", err)
		return ""
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("读取API响应失败: %v", err)
		return ""
	}

	var amapResp AmapResponse
	err = json.Unmarshal(body, &amapResp)
	if err != nil {
		log.Printf("解析API响应失败: %v", err)
		return ""
	}

	if amapResp.Status != "1" {
		log.Printf("API返回错误状态: %s", amapResp.Status)
		return ""
	}

	address := amapResp.Regeocode.AddressComponent.Province
	if len(amapResp.Regeocode.AddressComponent.City) > 0 {
		address += amapResp.Regeocode.AddressComponent.City[0]
	}
	address += amapResp.Regeocode.AddressComponent.District

	return address
}

func saveConfig(configJSON string) {
	err := os.WriteFile("config.json", []byte(configJSON), 0644)
	if err != nil {
		fmt.Println("保存配置文件时出错:", err)
	} else {
		fmt.Println("配置文件已成功保存到 config.json")
	}
}
