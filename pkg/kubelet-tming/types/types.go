package types

import "time"

type Timestamp struct {
	time	time.Time
}

func NewTimestamp() *Timestamp {
	return &Timestamp{time.Now()}
}


func ConvertToTimestamp(timeString string) *Timestamp {
	parsed, _ := time.Parse(time.RFC3339Nano, timeString)
	return &Timestamp{parsed}
}

func (t *Timestamp) Get() time.Time {
	return t.time
}

func (t *Timestamp) GetString() string {
	return t.time.Format(time.RFC3339Nano)
}