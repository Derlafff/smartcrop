package smartcrop

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"math"
	"os"
	"time"

	"github.com/nfnt/resize"
)

var (
	aspect                  = 0
	cropWidth               = 0.0
	cropHeight              = 0.0
	detailWeight            = 0.2
	skinColor               = [3]float64{0.78, 0.57, 0.44}
	skinBias                = 0.01
	skinBrightnessMin       = 0.2
	skinBrightnessMax       = 1.0
	skinThreshold           = 0.8
	skinWeight              = 1.8
	saturationBrightnessMin = 0.05
	saturationBrightnessMax = 0.9
	saturationThreshold     = 0.4
	saturationBias          = 0.2
	saturationWeight        = 0.3
	// step * minscale rounded down to the next power of two should be good
	scoreDownSample   = 8
	step              = 8
	scaleStep         = 0.1
	minScale          = 0.9
	maxScale          = 1.0
	edgeRadius        = 0.4
	edgeWeight        = -20.0
	outsideImportance = -0.5
	ruleOfThirds      = true
	prescale          = true
	prescalefactor    = 1.0
	debug             = false
)

type Score struct {
	Detail     float64
	Saturation float64
	Skin       float64
	Total      float64
}

type Crop struct {
	X      int
	Y      int
	Width  int
	Height int
	Score  Score
}

func SmartCrop(img *image.Image, width, height int) (Crop, image.Image, error) {
	if width == 0 && height == 0 {
		return Crop{}, nil, errors.New("Expect either a height or width")
	}

	scale := math.Min(float64((*img).Bounds().Size().X)/float64(width), float64((*img).Bounds().Size().Y)/float64(height))

	// resize image for faster processing
	var lowimg image.Image

	if prescale {

		if f := 1 / scale / minScale; f < 1 {
			prescalefactor = f
		}
		fmt.Println(prescalefactor)

		lowimg = resize.Resize(
			uint(float64((*img).Bounds().Size().X)*prescalefactor),
			0,
			*img,
			resize.NearestNeighbor)
		WriteImageToJpeg(&lowimg, "/tmp/prescale.jpg")

	} else {
		lowimg = *img
	}

	cropWidth, cropHeight = chop(float64(width)*scale*prescalefactor), chop(float64(height)*scale*prescalefactor)
	minScale = math.Min(maxScale, math.Max(1.0/scale, minScale))

	fmt.Printf("original resolution: %dx%d\n", (*img).Bounds().Size().X, (*img).Bounds().Size().Y)
	fmt.Printf("scale: %f, cropw: %f, croph: %f, minscale: %f\n", scale, cropWidth, cropHeight, minScale)

	//topCrop := analyse(img)
	topCrop := analyse(&lowimg)
	return topCrop, lowimg, nil
}

func chop(x float64) float64 {
	if x < 0 {
		return math.Ceil(x)
	}
	return math.Floor(x)
}

func thirds(x float64) float64 {
	x = (math.Mod(x-(1.0/3.0)+1.0, 2.0)*0.5 - 0.5) * 16.0
	return math.Max(1.0-x*x, 0.0)
}

func importance(crop *Crop, x, y int) float64 {
	if crop.X > x || x >= crop.X+crop.Width || crop.Y > y || y >= crop.Y+crop.Height {
		return outsideImportance
	}

	xf := float64(x-crop.X) / float64(crop.Width)
	yf := float64(y-crop.Y) / float64(crop.Height)

	px := math.Abs(0.5-xf) * 2.0
	py := math.Abs(0.5-yf) * 2.0

	dx := math.Max(px-1.0+edgeRadius, 0.0)
	dy := math.Max(py-1.0+edgeRadius, 0.0)
	d := (math.Pow(dx, 2) + math.Pow(dy, 2)) * edgeWeight

	s := 1.41 - math.Sqrt(math.Pow(px, 2)+math.Pow(py, 2))
	if ruleOfThirds {
		s += (math.Max(0.0, s+d+0.5) * 1.2) * (thirds(px) + thirds(py))
	}

	return s + d
}

func score(output *image.Image, crop *Crop) Score {
	height := (*output).Bounds().Size().Y
	width := (*output).Bounds().Size().X
	score := Score{}

	// FIXME DOWNSAMPLING

	for y := 0; y < height; y += 1 {
		for x := 0; x < width; x += 1 {

			r, g, b, _ := (*output).At(x, y).RGBA()

			r8 := float64(r >> 8)
			g8 := float64(g >> 8)
			b8 := float64(b >> 8)

			imp := importance(crop, int(x), int(y))
			det := g8 / 255.0

			score.Skin += r8 / 255.0 * (det + skinBias) * imp
			score.Detail += det * imp
			score.Saturation += b8 / 255.0 * (det + saturationBias) * imp
		}
	}

	score.Total = (score.Detail*detailWeight + score.Skin*skinWeight + score.Saturation*saturationWeight) / float64(crop.Width) / float64(crop.Height)
	return score
}

func WriteImageToJpeg(img *image.Image, name string) {
	fso, err := os.Create(name)
	if err != nil {
		panic(err)
	}
	defer fso.Close()

	jpeg.Encode(fso, (*img), &jpeg.Options{Quality: 90})
}

func analyse(img *image.Image) Crop {
	o := image.Image(image.NewRGBA((*img).Bounds()))

	now := time.Now()
	edgeDetect(img, &o)
	fmt.Println("Time elapsed edge:", time.Since(now))
	//WriteImageToJpeg(&o, "/tmp/smartcrop_edge.jpg")

	now = time.Now()
	skinDetect(img, &o)
	fmt.Println("Time elapsed skin:", time.Since(now))
	//WriteImageToJpeg(&o, "/tmp/smartcrop_skin.jpg")

	now = time.Now()
	saturationDetect(img, &o)
	fmt.Println("Time elapsed sat:", time.Since(now))
	//WriteImageToJpeg(&o, "/tmp/smartcrop_sat.jpg")

	now = time.Now()
	var topCrop Crop
	topScore := -1.0
	cs := crops(&o)
	fmt.Println("Time elapsed crops:", time.Since(now), len(cs))

	now = time.Now()
	for _, crop := range cs {
		//		nowIn := time.Now()
		crop.Score = score(&o, &crop)
		//		fmt.Println("Time elapsed single-score:", time.Since(nowIn))
		if crop.Score.Total > topScore {
			topCrop = crop
			topScore = crop.Score.Total
		}
	}
	fmt.Println("Time elapsed score:", time.Since(now))

	return topCrop
}

func saturation(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	r8 := float64(r >> 8)
	g8 := float64(g >> 8)
	b8 := float64(b >> 8)

	maximum := math.Max(math.Max(r8/255.0, g8/255.0), b8/255.0)
	minimum := math.Min(math.Min(r8/255.0, g8/255.0), b8/255.0)

	if maximum == minimum {
		return 0
	}

	l := (maximum + minimum) / 2.0
	d := maximum - minimum

	if l > 0.5 {
		return d / (2.0 - maximum - minimum)
	} else {
		return d / (maximum + minimum)
	}
}

func cie(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	r8 := float64(r >> 8)
	g8 := float64(g >> 8)
	b8 := float64(b >> 8)

	return 0.5126*b8 + 0.7152*g8 + 0.0722*r8
}

func skinCol(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	r8 := float64(r >> 8)
	g8 := float64(g >> 8)
	b8 := float64(b >> 8)

	mag := math.Sqrt(math.Pow(r8, 2) + math.Pow(g8, 2) + math.Pow(b8, 2))
	rd := r8/mag - skinColor[0]
	gd := g8/mag - skinColor[1]
	bd := b8/mag - skinColor[2]

	d := math.Sqrt(math.Pow(rd, 2) + math.Pow(gd, 2) + math.Pow(bd, 2))
	return 1.0 - d
}

func edgeDetect(i *image.Image, o *image.Image) {
	w := (*i).Bounds().Size().X
	h := (*i).Bounds().Size().Y

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var lightness float64

			if x == 0 || x >= w-1 || y == 0 || y >= h-1 {
				lightness = cie((*i).At(x, y))
			} else {
				lightness = cie((*i).At(x, y))*4.0 -
					cie((*i).At(x, y-1)) -
					cie((*i).At(x-1, y)) -
					cie((*i).At(x+1, y)) -
					cie((*i).At(x, y+1))
			}

			if lightness < 0 {
				continue
			}

			nc := color.RGBA{uint8(lightness), uint8(lightness), uint8(lightness), 255}
			(*o).(*image.RGBA).Set(x, y, nc)
		}
	}
}

func skinDetect(i *image.Image, o *image.Image) {
	w := (*i).Bounds().Size().X
	h := (*i).Bounds().Size().Y

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			lightness := cie((*i).At(x, y)) / 255.0
			skin := skinCol((*i).At(x, y))

			if skin > skinThreshold && lightness >= skinBrightnessMin && lightness <= skinBrightnessMax {
				r := (skin - skinThreshold) * (255.0 / (1.0 - skinThreshold))
				_, g, b, _ := (*o).At(x, y).RGBA()
				nc := color.RGBA{uint8(r), uint8(g >> 8), uint8(b >> 8), 255}
				(*o).(*image.RGBA).Set(x, y, nc)
			} else {
				_, g, b, _ := (*o).At(x, y).RGBA()
				nc := color.RGBA{0, uint8(g >> 8), uint8(b >> 8), 255}
				(*o).(*image.RGBA).Set(x, y, nc)
			}
		}
	}
}

func saturationDetect(i *image.Image, o *image.Image) {
	w := (*i).Bounds().Size().X
	h := (*i).Bounds().Size().Y

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			lightness := cie((*i).At(x, y)) / 255.0
			saturation := saturation((*i).At(x, y))

			if saturation > saturationThreshold && lightness >= saturationBrightnessMin && lightness <= saturationBrightnessMax {
				b := (saturation - saturationThreshold) * (255.0 / (1.0 - saturationThreshold))
				r, g, _, _ := (*o).At(x, y).RGBA()
				nc := color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b), 255}
				(*o).(*image.RGBA).Set(x, y, nc)
			} else {
				r, g, _, _ := (*o).At(x, y).RGBA()
				nc := color.RGBA{uint8(r >> 8), uint8(g >> 8), 0, 255}
				(*o).(*image.RGBA).Set(x, y, nc)
			}
		}
	}
}

func crops(i *image.Image) []Crop {
	res := []Crop{}
	width := (*i).Bounds().Size().X
	height := (*i).Bounds().Size().Y

	//minDimension := math.Min(float64(width), float64(height))
	cropW := cropWidth  //|| minDimension
	cropH := cropHeight //|| minDimension

	for scale := maxScale; scale >= minScale; scale -= scaleStep {
		for y := 0; float64(y)+cropH*scale <= float64(height); y += step {
			for x := 0; float64(x)+cropW*scale <= float64(width); x += step {
				res = append(res, Crop{
					X:      x,
					Y:      y,
					Width:  int(cropW * scale),
					Height: int(cropH * scale),
				})
			}
		}
	}

	return res
}
