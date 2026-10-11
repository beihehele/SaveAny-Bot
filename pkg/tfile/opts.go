package tfile

import "github.com/gotd/td/tg"

type TGFileOption func(*tgFile)

func WithMessage(msg *tg.Message) TGFileOption {
	return func(f *tgFile) {
		f.message = msg
	}
}

func WithName(name string) TGFileOption {
	return func(f *tgFile) {
		f.name = name
	}
}

func WithNameIfEmpty(name string) TGFileOption {
	return func(f *tgFile) {
		if f.name == "" {
			f.name = name
		}
	}
}
