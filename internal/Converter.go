package internal

import (
	"encoding/json"
	"strconv"
)

// BoolConverter handles custom JSON unmarshaling for boolean values
// It supports parsing booleans from true/false, strings, and numbers
type BoolConverter bool

// UnmarshalJSON implements the json.Unmarshaler interface for BoolConverter
func (b *BoolConverter) UnmarshalJSON(data []byte) error {
	// First, try to unmarshal as a regular boolean
	var directBool bool
	if err := json.Unmarshal(data, &directBool); err == nil {
		*b = BoolConverter(directBool)
		return nil
	}

	// If direct boolean unmarshaling fails, try as a string
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		parsedBool, err := strconv.ParseBool(str)
		if err == nil {
			*b = BoolConverter(parsedBool)
			return nil
		}
		return err
	}

	// If string unmarshaling fails, try as a number (int64 or float64)
	var numInt int64
	if err := json.Unmarshal(data, &numInt); err == nil {
		*b = BoolConverter(numInt != 0)
		return nil
	}

	var numFloat float64
	if err := json.Unmarshal(data, &numFloat); err == nil {
		*b = BoolConverter(numFloat != 0)
		return nil
	}

	return json.Unmarshal(data, b) // Fallback to default unmarshaling, likely to fail but included for completeness
}

// MarshalJSON implements the json.Marshaler interface for BoolConverter
func (b BoolConverter) MarshalJSON() ([]byte, error) {
	return json.Marshal(bool(b))
}
