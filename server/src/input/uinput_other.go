//go:build !linux

package input

import "errors"

func newUinput() (Backend, error) {
	return nil, errors.New("uinput is only available on Linux")
}
