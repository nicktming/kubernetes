package config


type SourcesReady interface {
	AddSource(source string)

	AllReady() bool
}