package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// JSON wraps json.RawMessage with database Scanner/Valuer support for GORM.
type JSON json.RawMessage

func (j JSON) Value() (driver.Value, error) {
	if len(j) == 0 {
		return nil, nil
	}
	return string(j), nil
}

func (j *JSON) Scan(value any) error {
	if value == nil {
		*j = nil
		return nil
	}
	switch v := value.(type) {
	case []byte:
		*j = make(JSON, len(v))
		copy(*j, v)
	case string:
		*j = JSON(v)
	default:
		return fmt.Errorf("json: unsupported scan type %T", value)
	}
	return nil
}

func (j JSON) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	return []byte(j), nil
}

func (j *JSON) UnmarshalJSON(data []byte) error {
	if data == nil || string(data) == "null" {
		*j = nil
		return nil
	}
	*j = make(JSON, len(data))
	copy(*j, data)
	return nil
}

func (JSON) GormDataType() string { return "text" }

// Int64Slice 是支持 GORM 存储的 int64 切片（JSON 格式存入数据库）。
type Int64Slice []int64

func (s Int64Slice) Value() (driver.Value, error) {
	if len(s) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal([]int64(s))
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func (s *Int64Slice) Scan(value any) error {
	if value == nil {
		*s = nil
		return nil
	}
	var data []byte
	switch v := value.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return fmt.Errorf("int64slice: unsupported scan type %T", value)
	}
	if len(data) == 0 || string(data) == "null" {
		*s = nil
		return nil
	}
	return json.Unmarshal(data, (*[]int64)(s))
}

func (Int64Slice) GormDataType() string { return "text" }
