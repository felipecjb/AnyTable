//go:build !linux

package capture

import "errors"

func shmAlloc(size int) (int, []byte, error) {
	return 0, nil, errors.New("capture: SysV shared memory is only available on Linux")
}

func shmRemove(id int) {}

func shmDetach(mem []byte) {}
