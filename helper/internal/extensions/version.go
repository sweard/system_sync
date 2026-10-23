package extensions

import (
	"cmp"
	"strings"
)

// Compare the accepted extension versions by SemVer precedence, including
// numeric prerelease identifiers: https://semver.org/#spec-item-11.
func compareVersions(a, b string) int {
	coreA, preA, _ := strings.Cut(a, "-")
	coreB, preB, _ := strings.Cut(b, "-")
	partsA, partsB := strings.Split(coreA, "."), strings.Split(coreB, ".")
	for i := range partsA {
		if order := compareNumeric(partsA[i], partsB[i]); order != 0 {
			return order
		}
	}
	if preA == preB {
		return 0
	}
	if preA == "" {
		return 1
	}
	if preB == "" {
		return -1
	}
	partsA, partsB = strings.Split(preA, "."), strings.Split(preB, ".")
	for i := 0; i < len(partsA) && i < len(partsB); i++ {
		a, b := partsA[i], partsB[i]
		numericA := a != "" && strings.Trim(a, "0123456789") == ""
		numericB := b != "" && strings.Trim(b, "0123456789") == ""
		order := strings.Compare(a, b)
		switch {
		case numericA && numericB:
			order = compareNumeric(a, b)
		case numericA:
			order = -1
		case numericB:
			order = 1
		}
		if order != 0 {
			return order
		}
	}
	return cmp.Compare(len(partsA), len(partsB))
}

func compareNumeric(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if order := cmp.Compare(len(a), len(b)); order != 0 {
		return order
	}
	return strings.Compare(a, b)
}
