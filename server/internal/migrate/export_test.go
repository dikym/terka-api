package migrate

import (
	"testing"
	"time"
)

const LockKey = lockKey

func SetLockTimeouts(t *testing.T, wait, file time.Duration) {
	oldWait, oldFile := lockWait, fileLockTimeout
	lockWait, fileLockTimeout = wait, file
	t.Cleanup(func() { lockWait, fileLockTimeout = oldWait, oldFile })
}
