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
	"gopkg.in/yaml.v3"
)

// Config 结构体用于存储配置信息
type Config struct {
	OutputFolder      string `yaml:"outputFolder"`
	NoExifFolder      string `yaml:"noExifFolder"`
	JpegQuality       int    `yaml:"jpegQuality"`
	AmapAPIKey        string `yaml:"amapAPIKey"`
	MaxConcurrency    int    `yaml:"maxConcurrency"`
	FontPath          string `yaml:"fontPath"`
	TimeFormat        string `yaml:"timeFormat"`
	ShowFullAddress   bool   `yaml:"showFullAddress"` // 是否显示完整地址
	WatermarkSettings struct {
		FontSize      float64 `yaml:"fontSize"`
		WidthPadding  float64 `yaml:"widthPadding"`
		HeightPadding float64 `yaml:"heightPadding"`
		Color         struct {
			R uint8 `yaml:"r"`
			G uint8 `yaml:"g"`
			B uint8 `yaml:"b"`
			A uint8 `yaml:"a"`
		} `yaml:"color"`
	} `yaml:"watermarkSettings"`
}

const configYAML = `# ==================== 基础设置 ====================
# 处理完成的图片输出目录
outputFolder: "已处理"
# 无EXIF信息的图片存放目录
noExifFolder: "无EXIF信息"
# JPEG压缩质量 (1-100, 数值越大质量越好, 文件越大)
jpegQuality: 70
# 并发处理数量 (根据CPU性能调整)
maxConcurrency: 5

# ==================== 字体设置 ====================
# 字体文件路径 (Windows默认微软雅黑)
fontPath: "C:/Windows/Fonts/msyh.ttc"

# ==================== 时间格式 ====================
# Go语言时间格式模板 (2006-01-02 15:04:05 是固定写法)
timeFormat: "2006-01-02 15:04:05"

# ==================== 地理位置设置 ====================
# 高德地图API密钥 (获取地址: https://console.amap.com/)
amapAPIKey: ""

# 是否显示完整地址 (true=完整地址, false=省市区)
showFullAddress: false

# ==================== 水印设置 ====================
watermarkSettings:
  # 字体大小 (相对于图片长边的比例, 0.02 = 2%)
  fontSize: 0.02
  # 水印距离图片右边界的距离 (相对于图片宽度的比例)
  widthPadding: 0.02
  # 水印距离图片底部的距离 (相对于图片高度的比例)
  heightPadding: 0.01

  # 水印颜色 (RGBA格式, 0-255)
  color:
    r: 255  # 红色分量
    g: 165  # 绿色分量
    b: 0    # 蓝色分量
    a: 255  # 透明度 (255=不透明, 0=完全透明)
`

// AmapResponse 定义高德地图API的响应结构
type AmapResponse struct {
	Status    string `json:"status"`
	Regeocode struct {
		FormattedAddress string `json:"formatted_address"` // 完整地址
		AddressComponent struct {
			Province string      `json:"province"`
			City     interface{} `json:"city"` // 兼容字符串或数组
			District string      `json:"district"`
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
	data, err := os.ReadFile("config.yaml")
	if err != nil {
		return fmt.Errorf("打开配置文件失败: %v", err)
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("解析配置文件失败: %v", err)
	}
	return nil
}

func main() {
	fmt.Println("开始处理图片,若有问题请检查process.log")
	if err := LoadConfig(); err != nil {
		saveConfig(configYAML)
		log.Fatalf("加载配置失败: %v", err)
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
	fmt.Println("jpg文件数量:", len(files))

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
	fmt.Println("程序运行结束，按下回车键退出...")
	fmt.Scanln() // 等待用户输入
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
			log.Printf("无法获取 GPS 数据: %v", err)
			addressChan <- ""
			return
		}
		log.Printf("解析到的 GPS 坐标: lat=%f, long=%f", lat, long)

		address := getAddressFromGPS(lat, long)
		log.Printf("获取的地址: %s", address)
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

	timeLayout := config.TimeFormat
	if timeLayout == "" {
		timeLayout = "2006-01-02 15:04:05"
	}
	watermarkText := fmt.Sprintf("%s\n%s", timeStr.Format(timeLayout), address)
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

// 计算单个字符的显示宽度
func calcCharWidth(r rune, fontSize float64) float64 {
	if r > 127 {
		return fontSize * 1 // 中文等宽字符
	}
	return fontSize * 0.5 // ASCII窄字符
}

// 计算一行文本的总宽度
func calcTextWidth(line string, fontSize float64) int {
	var width float64
	for _, r := range line {
		width += calcCharWidth(r, fontSize)
	}
	return int(width)
}

// 自动换行：将超长文本拆分为多行
func wrapText(lines []string, fontSize, maxWidth float64) []string {
	var result []string
	for _, line := range lines {
		runes := []rune(line)
		if len(runes) == 0 {
			result = append(result, line)
			continue
		}

		var currentLine []rune
		var currentWidth float64
		for _, r := range runes {
			charWidth := calcCharWidth(r, fontSize)
			if currentWidth+charWidth > maxWidth && len(currentLine) > 0 {
				result = append(result, string(currentLine))
				currentLine, currentWidth = []rune{r}, charWidth
			} else {
				currentLine = append(currentLine, r)
				currentWidth += charWidth
			}
		}
		if len(currentLine) > 0 {
			result = append(result, string(currentLine))
		}
	}
	return result
}

// 绘制多行文本（带偏移量，用于描边/阴影效果）
func drawLines(c *freetype.Context, lines []string, lineWidths []int, rightEdge, startY, lineHeight int, fontSize float64, offsets []struct{ dx, dy int }) {
	y := startY
	for i, line := range lines {
		x := rightEdge - lineWidths[i]
		for _, off := range offsets {
			pt := freetype.Pt(x+off.dx, y+int(fontSize)+off.dy)
			c.DrawString(line, pt)
		}
		y += lineHeight
	}
}

func addWatermark(img image.Image, text string) image.Image {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	// 根据长边计算字体大小
	maxSide := max(width, height)
	fontSize := float64(maxSide) * config.WatermarkSettings.FontSize
	lineHeight := int(fontSize * 1.2)

	widthPadding := int(float64(width) * config.WatermarkSettings.WidthPadding)
	heightPadding := int(float64(height) * config.WatermarkSettings.HeightPadding)

	// 创建绘图画布
	rgba := image.NewRGBA(bounds)
	draw.Draw(rgba, bounds, img, bounds.Min, draw.Src)

	// 加载字体
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

	// 文本换行处理
	maxTextWidth := float64(width) * 0.6
	lines := wrapText(strings.Split(text, "\n"), fontSize, maxTextWidth)

	// 计算每行宽度（用于右对齐）
	lineWidths := make([]int, len(lines))
	for i, line := range lines {
		lineWidths[i] = calcTextWidth(line, fontSize)
	}

	// 计算绘制位置
	rightEdge := bounds.Max.X - widthPadding
	startY := bounds.Max.Y - (lineHeight * len(lines)) - heightPadding

	// 初始化绘图上下文
	c := freetype.NewContext()
	c.SetDPI(72)
	c.SetFont(font)
	c.SetFontSize(fontSize)
	c.SetClip(bounds)
	c.SetDst(rgba)

	// 透明度设置
	textAlpha := config.WatermarkSettings.Color.A
	shadowAlpha := uint8(float64(textAlpha) * 0.7)

	// 1. 绘制描边
	strokeOffsets := []struct{ dx, dy int }{
		{-2, -2}, {-2, 0}, {-2, 2}, {0, -2}, {0, 2}, {2, -2}, {2, 0}, {2, 2},
	}
	c.SetSrc(image.NewUniform(color.NRGBA{0, 0, 0, textAlpha}))
	drawLines(c, lines, lineWidths, rightEdge, startY, lineHeight, fontSize, strokeOffsets)

	// 2. 绘制阴影
	shadowOffsets := []struct{ dx, dy int }{{4, 4}, {3, 3}, {5, 5}}
	c.SetSrc(image.NewUniform(color.NRGBA{0, 0, 0, shadowAlpha}))
	drawLines(c, lines, lineWidths, rightEdge, startY, lineHeight, fontSize, shadowOffsets)

	// 3. 绘制主文本
	mainColor := color.NRGBA{
		config.WatermarkSettings.Color.R,
		config.WatermarkSettings.Color.G,
		config.WatermarkSettings.Color.B,
		textAlpha,
	}
	c.SetSrc(image.NewUniform(mainColor))
	drawLines(c, lines, lineWidths, rightEdge, startY, lineHeight, fontSize, []struct{ dx, dy int }{{0, 0}})

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

// CoordinateConvertResponse 定义高德坐标转换API的响应结构
type CoordinateConvertResponse struct {
	Status    string `json:"status"`
	Info      string `json:"info"`
	Locations string `json:"locations"`
}

// 将GPS坐标转换为高德坐标
func convertGPSToAmap(lat, long float64) (float64, float64, error) {
	if len(config.AmapAPIKey) == 0 {
		return 0, 0, fmt.Errorf("API Key 为空")
	}

	url := fmt.Sprintf("https://restapi.amap.com/v3/assistant/coordinate/convert?key=%s&locations=%.6f,%.6f&coordsys=gps",
		config.AmapAPIKey, long, lat)

	resp, err := http.Get(url)
	if err != nil {
		return 0, 0, fmt.Errorf("坐标转换API请求失败: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, fmt.Errorf("读取坐标转换API响应失败: %v", err)
	}

	var convertResp CoordinateConvertResponse
	if err := json.Unmarshal(body, &convertResp); err != nil {
		return 0, 0, fmt.Errorf("解析坐标转换API响应失败: %v", err)
	}

	if convertResp.Status != "1" {
		return 0, 0, fmt.Errorf("坐标转换API返回错误: %s", convertResp.Info)
	}

	// 解析返回的坐标（格式: "经度,纬度;"）
	locations := strings.TrimSuffix(convertResp.Locations, ";")
	coords := strings.Split(locations, ",")
	if len(coords) != 2 {
		return 0, 0, fmt.Errorf("坐标格式解析失败: %s", convertResp.Locations)
	}

	var newLong, newLat float64
	if _, err := fmt.Sscanf(coords[0], "%f", &newLong); err != nil {
		return 0, 0, fmt.Errorf("解析经度失败: %v", err)
	}
	if _, err := fmt.Sscanf(coords[1], "%f", &newLat); err != nil {
		return 0, 0, fmt.Errorf("解析纬度失败: %v", err)
	}

	log.Printf("坐标转换成功: GPS(%.6f,%.6f) -> 高德(%.6f,%.6f)", long, lat, newLong, newLat)
	return newLat, newLong, nil
}

// 通过经纬度调用高德API获取地址
func getAddressFromGPS(lat, long float64) string {
	if len(config.AmapAPIKey) == 0 {
		log.Println("API Key 为空")
		return ""
	}

	// 先将GPS坐标转换为高德坐标
	convertedLat, convertedLong, err := convertGPSToAmap(lat, long)
	if err != nil {
		log.Printf("坐标转换失败，使用原始坐标: %v", err)
		convertedLat = lat
		convertedLong = long
	}

	url := fmt.Sprintf("https://restapi.amap.com/v3/geocode/regeo?output=JSON&location=%.6f,%.6f&key=%s&radius=10", convertedLong, convertedLat, config.AmapAPIKey)

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
		log.Printf("解析 API 响应失败，状态码: %d，响应体内容: %s，错误信息: %v", resp.StatusCode, string(body), err)
		return ""
	}

	if amapResp.Status != "1" {
		log.Printf("API返回错误状态: %s", amapResp.Status)
		return ""
	}

	// 如果配置为显示完整地址，直接返回 formatted_address
	if config.ShowFullAddress {
		return amapResp.Regeocode.FormattedAddress
	}

	// 否则返回简略地址：省 + 市 + 区
	address := amapResp.Regeocode.AddressComponent.Province

	// 处理 city 可能是字符串或数组的情况
	var cityName string
	switch city := amapResp.Regeocode.AddressComponent.City.(type) {
	case string:
		cityName = city
	case []interface{}:
		if len(city) > 0 {
			if str, ok := city[0].(string); ok {
				cityName = str
			}
		}
	}

	if cityName != "" {
		address += cityName
	}
	address += amapResp.Regeocode.AddressComponent.District

	return address
}

func saveConfig(configYAML string) {
	err := os.WriteFile("config.yaml", []byte(configYAML), 0644)
	if err != nil {
		fmt.Println("生成配置文件时出错:", err)
	} else {
		fmt.Println("没有找到配置文件，已重新生成配置文件 config.yaml")
	}
}
