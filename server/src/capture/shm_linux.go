//go:build linux

package capture

import "golang.org/x/sys/unix"

// shmAlloc creates and attaches a private SysV shared memory segment (mode 0600).
func shmAlloc(size int) (int, []byte, error) {
	id, err := unix.SysvShmGet(unix.IPC_PRIVATE, size, unix.IPC_CREAT|0o600)
	if err != nil {
		return 0, nil, err
	}
	mem, err := unix.SysvShmAttach(id, 0, 0)
	if err != nil {
		_, _ = unix.SysvShmCtl(id, unix.IPC_RMID, nil)
		return 0, nil, err
	}
	return id, mem, nil
}

// shmRemove marks the segment for destruction; it lives until every process detaches,
// so a crashed daemon never leaks it.
func shmRemove(id int) { _, _ = unix.SysvShmCtl(id, unix.IPC_RMID, nil) }

func shmDetach(mem []byte) { _ = unix.SysvShmDetach(mem) }
