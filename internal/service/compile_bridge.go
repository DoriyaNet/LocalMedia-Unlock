package service

import (
	"localmedia-unlock/internal/compile"
	"localmedia-unlock/internal/model"
)

func compileConfig(cfg model.Config) ([]byte, error) {
	return compile.Compile(cfg)
}
