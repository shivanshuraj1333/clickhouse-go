// Licensed to ClickHouse, Inc. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. ClickHouse, Inc. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package column

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/ch-go/proto"

	"github.com/ClickHouse/clickhouse-go/v2/lib/timezone"
)

const (
	defaultTime64Format          = "15:04:05.999999999"
	binaryTypeTime64UTC          = 0x34
	binaryTypeTime64WithTimezone = 0x35
)

// Time64 implements ClickHouse Time64 (Int64, sub-second, scale 0-9) column with optional timezone.
// Stores time-of-day only, no date component. Supports negative values and multiple input formats.
type Time64 struct {
	chType   Type
	timezone *time.Location
	name     string
	col      proto.ColTime64
}

func (col *Time64) Reset() {
	col.col.Reset()
}

func (col *Time64) Name() string {
	return col.name
}

// parse parses the ClickHouse type definition and sets precision and timezone if present.
func (col *Time64) parse(t Type, tz *time.Location) (_ Interface, err error) {
	col.chType = t
	// Handle Time64(6) format
	if strings.HasPrefix(string(t), "Time64(") {
		// Check if it has timezone parameter
		if strings.Contains(string(t), ",") {
			// Format: Time64(6, 'UTC')
			parts := strings.Split(string(t), ",")
			if len(parts) != 2 {
				return nil, &UnsupportedColumnTypeError{t: t}
			}

			// Parse precision
			precisionStr := strings.TrimSuffix(strings.TrimPrefix(parts[0], "Time64("), ")")
			precision, err := strconv.ParseInt(precisionStr, 10, 8)
			if err != nil {
				return nil, err
			}
			p := byte(precision)
			col.col.WithPrecision(proto.Precision(p))

			// Parse timezone
			timezoneName := strings.TrimSuffix(strings.TrimPrefix(parts[1], " '"), "')")
			timezone, err := timezone.Load(timezoneName)
			if err != nil {
				return nil, err
			}
			col.timezone = timezone
			return col, nil
		} else {
			// Format: Time64(6)
			params := strings.TrimSuffix(strings.TrimPrefix(string(t), "Time64("), ")")
			precision, err := strconv.ParseInt(params, 10, 8)
			if err != nil {
				return nil, err
			}
			p := byte(precision)
			col.col.WithPrecision(proto.Precision(p))
			col.timezone = tz
			return col, nil
		}
	}
	return nil, &UnsupportedColumnTypeError{
		t: t,
	}
}

func (col *Time64) Type() Type {
	return col.chType
}

func (col *Time64) ScanType() reflect.Type {
	return scanTypeTime
}

func (col *Time64) Precision() (int64, bool) {
	return int64(col.col.Precision), col.col.PrecisionSet
}

func (col *Time64) Rows() int {
	return col.col.Rows()
}

func (col *Time64) Row(i int, ptr bool) any {
	value := col.row(i)
	if ptr {
		return &value
	}
	return value
}

func (col *Time64) ScanRow(dest any, row int) error {
	switch d := dest.(type) {
	case *time.Time:
		*d = col.row(row)
	case **time.Time:
		*d = new(time.Time)
		**d = col.row(row)
	case *int64:
		// Convert time.Time to milliseconds since midnight (can be negative)
		t := col.row(row)
		*d = int64(t.Hour()*3600000 + t.Minute()*60000 + t.Second()*1000 + t.Nanosecond()/1000000)
	case **int64:
		*d = new(int64)
		// Convert time.Time to milliseconds since midnight (can be negative)
		t := col.row(row)
		**d = int64(t.Hour()*3600000 + t.Minute()*60000 + t.Second()*1000 + t.Nanosecond()/1000000)
	case *sql.NullTime:
		return d.Scan(col.row(row))
	default:
		if scan, ok := dest.(sql.Scanner); ok {
			return scan.Scan(col.row(row))
		}
		return &ColumnConverterError{
			Op:   "ScanRow",
			To:   fmt.Sprintf("%T", dest),
			From: "Time64",
		}
	}
	return nil
}

func (col *Time64) Append(v any) (nulls []uint8, err error) {
	switch v := v.(type) {
	case []int64:
		nulls = make([]uint8, len(v))
		for i := range v {
			// Convert milliseconds since midnight to time.Time (can be negative)
			milliseconds := v[i]
			seconds := milliseconds / 1000
			hours := seconds / 3600
			minutes := (seconds % 3600) / 60
			secs := seconds % 60
			nsecs := (milliseconds % 1000) * 1000000
			col.col.Append(time.Date(1970, 1, 1, int(hours), int(minutes), int(secs), int(nsecs), time.UTC))
		}
	case []*int64:
		nulls = make([]uint8, len(v))
		for i := range v {
			switch {
			case v[i] != nil:
				// Convert milliseconds since midnight to time.Time (can be negative)
				milliseconds := *v[i]
				seconds := milliseconds / 1000
				hours := seconds / 3600
				minutes := (seconds % 3600) / 60
				secs := seconds % 60
				nsecs := (milliseconds % 1000) * 1000000
				col.col.Append(time.Date(1970, 1, 1, int(hours), int(minutes), int(secs), int(nsecs), time.UTC))
			default:
				col.col.Append(time.Time{})
				nulls[i] = 1
			}
		}
	case []time.Time:
		nulls = make([]uint8, len(v))
		for i := range v {
			col.col.Append(v[i])
		}
	case []*time.Time:
		nulls = make([]uint8, len(v))
		for i := range v {
			switch {
			case v[i] != nil:
				col.col.Append(*v[i])
			default:
				col.col.Append(time.Time{})
				nulls[i] = 1
			}
		}
	case []string:
		nulls = make([]uint8, len(v))
		for i := range v {
			value, err := col.parseTime(v[i])
			if err != nil {
				return nil, err
			}
			col.col.Append(value)
		}
	case []sql.NullTime:
		nulls = make([]uint8, len(v))
		for i := range v {
			col.AppendRow(v[i])
		}
	case []*sql.NullTime:
		nulls = make([]uint8, len(v))
		for i := range v {
			if v[i] == nil {
				nulls[i] = 1
			}
			col.AppendRow(v[i])
		}
	default:
		if valuer, ok := v.(driver.Valuer); ok {
			val, err := valuer.Value()
			if err != nil {
				return nil, &ColumnConverterError{
					Op:   "Append",
					To:   "Time64",
					From: fmt.Sprintf("%T", v),
					Hint: "could not get driver.Valuer value",
				}
			}
			return col.Append(val)
		}
		return nil, &ColumnConverterError{
			Op:   "Append",
			To:   "Time64",
			From: fmt.Sprintf("%T", v),
		}
	}
	return
}

// AppendRow appends a value to the column. Accepts time.Time, int64 (milliseconds), string, or driver.Valuer.
func (col *Time64) AppendRow(v any) error {
	switch v := v.(type) {
	case int64:
		// Convert milliseconds since midnight to time.Time (can be negative)
		milliseconds := v
		seconds := milliseconds / 1000
		hours := seconds / 3600
		minutes := (seconds % 3600) / 60
		secs := seconds % 60
		nsecs := (milliseconds % 1000) * 1000000
		col.col.Append(time.Date(1970, 1, 1, int(hours), int(minutes), int(secs), int(nsecs), time.UTC))
	case *int64:
		switch {
		case v != nil:
			// Convert milliseconds since midnight to time.Time (can be negative)
			milliseconds := *v
			seconds := milliseconds / 1000
			hours := seconds / 3600
			minutes := (seconds % 3600) / 60
			secs := seconds % 60
			nsecs := (milliseconds % 1000) * 1000000
			col.col.Append(time.Date(1970, 1, 1, int(hours), int(minutes), int(secs), int(nsecs), time.UTC))
		default:
			col.col.Append(time.Time{})
		}
	case time.Time:
		col.col.Append(v)
	case *time.Time:
		switch {
		case v != nil:
			col.col.Append(*v)
		default:
			col.col.Append(time.Time{})
		}
	case sql.NullTime:
		switch v.Valid {
		case true:
			col.col.Append(v.Time)
		default:
			col.col.Append(time.Time{})
		}
	case *sql.NullTime:
		switch v.Valid {
		case true:
			col.col.Append(v.Time)
		default:
			col.col.Append(time.Time{})
		}
	case string:
		timeValue, err := col.parseTime(v)
		if err != nil {
			return err
		}
		col.col.Append(timeValue)
	case nil:
		col.col.Append(time.Time{})
	default:
		if valuer, ok := v.(driver.Valuer); ok {
			val, err := valuer.Value()
			if err != nil {
				return &ColumnConverterError{
					Op:   "AppendRow",
					To:   "Time64",
					From: fmt.Sprintf("%T", v),
					Hint: "could not get driver.Valuer value",
				}
			}
			return col.AppendRow(val)
		}
		return &ColumnConverterError{
			Op:   "AppendRow",
			To:   "Time64",
			From: fmt.Sprintf("%T", v),
		}
	}
	return nil
}

func (col *Time64) Decode(reader *proto.Reader, rows int) error {
	return col.col.DecodeColumn(reader, rows)
}

func (col *Time64) Encode(buffer *proto.Buffer) {
	col.col.EncodeColumn(buffer)
}

func (col *Time64) row(i int) time.Time {
	time := col.col.Row(i)
	if col.timezone != nil {
		time = time.In(col.timezone)
	}
	return time
}

func (col *Time64) parseTime(value string) (tv time.Time, err error) {
	// Try multiple time formats with precision
	formats := []string{
		"15:04:05",
		"15:04",
		"15:04:05.999",
		"15:04:05.999999",
		"15:04:05.999999999",
		"3:04:05 PM",
		"3:04 PM",
		"15:04:05 -07:00",
		"15:04:05.999 -07:00",
		"15:04:05.999999 -07:00",
		"15:04:05.999999999 -07:00",
	}

	for _, format := range formats {
		if tv, err = time.Parse(format, value); err == nil {
			// Extract only the time part and use the column's timezone if set
			timezone := time.UTC
			if col.timezone != nil {
				timezone = col.timezone
			}
			return time.Date(1970, 1, 1, tv.Hour(), tv.Minute(), tv.Second(), tv.Nanosecond(), timezone), nil
		}
	}

	// Try parsing as milliseconds since midnight
	if milliseconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		seconds := milliseconds / 1000
		hours := seconds / 3600
		minutes := (seconds % 3600) / 60
		secs := seconds % 60
		nsecs := (milliseconds % 1000) * 1000000
		timezone := time.UTC
		if col.timezone != nil {
			timezone = col.timezone
		}
		return time.Date(1970, 1, 1, int(hours), int(minutes), int(secs), int(nsecs), timezone), nil
	}

	return time.Time{}, fmt.Errorf("cannot parse time64 value: %s", value)
}

var _ Interface = (*Time64)(nil)
var _ CustomSerialization = (*Time64)(nil)

// WriteStatePrefix is a no-op for Time64
func (col *Time64) WriteStatePrefix(buffer *proto.Buffer) error {
	return nil
}

// ReadStatePrefix is a no-op for Time64
func (col *Time64) ReadStatePrefix(reader *proto.Reader) error {
	return nil
}
