package cm

type CgroupName []string


type CgroupManager interface {
	Name(name CgroupName) string
}