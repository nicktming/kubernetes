package cm

import (
	"testing"
)

func TestNewCgroupName(t *testing.T) {

	subsystems, err := GetCgroupSubsystems()
	if err != nil {
		t.Fatalf("failed to get mounted cgroup subsystems: %v", err)
	}

	CgroupDriver := "cgroupfs"
	cgroupManager := NewCgroupManager(subsystems, CgroupDriver)

	size := int64(50 * 1024 * 1024)

	config := &CgroupConfig{
		Name: 	[]string{"nicktming"},
		ResourceParameters: &ResourceConfig{
			Memory: 	&size,
		},
	}

	err = cgroupManager.Create(config)

	if err != nil {
		t.Fatal(err)
	}

}