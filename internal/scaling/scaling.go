package scaling

import (
	"math"
	"strconv"
	"strings"
)

const (
	MinScale = 0.25
	MaxScale = 4.0

	precision          = 5
	hyprlandScaleSteps = 120
)

func Clamp(value float64) float64 {
	switch {
	case math.IsNaN(value) || math.IsInf(value, 0) || value <= 0:
		return 1
	case value < MinScale:
		return MinScale
	case value > MaxScale:
		return MaxScale
	default:
		return value
	}
}

func Default(value float64) float64 {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 1
	}
	return value
}

func Sharp(width, height int, value float64) bool {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return false
	}
	value = Round(value)
	if width <= 0 || height <= 0 {
		return true
	}
	if exactInteger(float64(width)/value) && exactInteger(float64(height)/value) {
		return true
	}
	numerator := int(math.Round(value * hyprlandScaleSteps))
	if numerator <= 0 || Round(float64(numerator)/hyprlandScaleSteps) != value {
		return false
	}
	return sharpAtNumerator(width, height, numerator)
}

func ClosestSharp(width, height int, value float64) (float64, bool) {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) || width <= 0 || height <= 0 {
		return 0, false
	}
	return nearestSharpScale(width, height, Round(value))
}

// GridScales returns the scales on Hyprland's 1/120 correction grid that
// produce integer logical dimensions for the requested mode.
func GridScales(width, height int, minScale, maxScale float64) []float64 {
	if width <= 0 || height <= 0 || math.IsNaN(minScale) || math.IsNaN(maxScale) || minScale > maxScale {
		return nil
	}
	minScale = math.Max(minScale, MinScale)
	maxScale = math.Min(maxScale, MaxScale)
	if minScale > maxScale {
		return nil
	}

	minNumerator := int(math.Ceil(minScale * hyprlandScaleSteps))
	maxNumerator := int(math.Floor(maxScale * hyprlandScaleSteps))
	scales := make([]float64, 0, maxNumerator-minNumerator+1)
	for numerator := minNumerator; numerator <= maxNumerator; numerator++ {
		if sharpAtNumerator(width, height, numerator) {
			scales = append(scales, Round(float64(numerator)/hyprlandScaleSteps))
		}
	}
	return scales
}

func LogicalSize(width, height int, scale float64) (float64, float64) {
	scale = Round(Default(scale))
	return float64(width) / scale, float64(height) / scale
}

func Format(value float64) string {
	value = Round(Default(value))
	s := strconv.FormatFloat(value, 'f', precision, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}

func Round(value float64) float64 {
	return round(value, precision)
}

func nearestSharpScale(width, height int, value float64) (float64, bool) {
	best := 0.0
	bestDistance := math.Inf(1)

	for _, candidate := range GridScales(width, height, MinScale, MaxScale) {
		distance := math.Abs(candidate - value)
		if distance < bestDistance {
			best = candidate
			bestDistance = distance
		}
	}

	if bestDistance == math.Inf(1) {
		return 0, false
	}
	return Round(best), true
}

func sharpAtNumerator(width, height, numerator int) bool {
	return (hyprlandScaleSteps*width)%numerator == 0 && (hyprlandScaleSteps*height)%numerator == 0
}

func exactInteger(value float64) bool {
	return value == math.Round(value)
}

func round(value float64, places int) float64 {
	factor := math.Pow10(places)
	return math.Round(value*factor) / factor
}
