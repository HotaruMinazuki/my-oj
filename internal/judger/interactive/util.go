package interactive

import (
	"io"
	"os"
)

func openSharedFile(path string) (io.ReadCloser, error) {
	return os.Open(path)
}
