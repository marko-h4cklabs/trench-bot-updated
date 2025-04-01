package utils

import (
	"bytes"
	"strings"
)

// ToUpperCase converts a string to uppercase
func ToUpperCase(input string) string {
	return strings.ToUpper(input)
}

// NewRequestBuffer creates a new *bytes.Buffer from a byte slice
func NewRequestBuffer(data []byte) *bytes.Buffer {
	return bytes.NewBuffer(data)
}
