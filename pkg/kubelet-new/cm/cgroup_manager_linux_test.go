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

	config := &CgroupConfig{
		Name: 	"nicktming",
		ResourceParameters: &ResourceConfig{
			Memory: 	"50MB",
		},
	}

	err = cgroupManager.Create(config)

	if err != nil {
		t.Fatal(err)
	}

}