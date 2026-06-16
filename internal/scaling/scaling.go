package scaling

import (
	"math"
	"strconv"
	"strings"
)

const (
	MinScale = 0.25
	MaxScale = 4.0

	precision        = 5
	integerTolerance = 0.01
	maxDenominator   = 10
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
	return nearlyInteger(float64(width)/value) && nearlyInteger(float64(height)/value)
}

func ClosestSharp(width, height int, value float64) (float64, bool) {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) || width <= 0 || height <= 0 {
		return 0, false
	}
	return nearestSharpScale(width, height, Round(value))
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
	bestDenominator := 0

	for denominator := 1; denominator <= maxDenominator; denominator++ {
		minNumerator := int(math.Ceil(MinScale * float64(denominator)))
		maxNumerator := int(math.Floor(MaxScale * float64(denominator)))
		for numerator := minNumerator; numerator <= maxNumerator; numerator++ {
			candidate := float64(numerator) / float64(denominator)
			if !Sharp(width, height, candidate) {
				continue
			}

			distance := math.Abs(candidate - value)
			if distance < bestDistance || (distance == bestDistance && denominator < bestDenominator) {
				best = candidate
				bestDistance = distance
				bestDenominator = denominator
			}
		}
	}

	if bestDistance == math.Inf(1) {
		return 0, false
	}
	return Round(best), true
}

func nearlyInteger(value float64) bool {
	return math.Abs(value-math.Round(value)) <= integerTolerance
}

func round(value float64, places int) float64 {
	factor := math.Pow10(places)
	return math.Round(value*factor) / factor
}
