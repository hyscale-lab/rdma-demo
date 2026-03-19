package ingest

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
)

const digestBufferSize = 128 * 1024

type Result struct {
	ETag string
	Size int64
}

func ConsumeReader(body io.ReadCloser, maxObjectSize int64) (Result, error) {
	defer body.Close()

	hasher := md5.New()
	buf := make([]byte, digestBufferSize)
	var size int64

	for {
		n, err := body.Read(buf)
		if n > 0 {
			size += int64(n)
			if maxObjectSize > 0 && size > maxObjectSize {
				return Result{}, fmt.Errorf("object too large: %d > %d", size, maxObjectSize)
			}
			if _, writeErr := hasher.Write(buf[:n]); writeErr != nil {
				return Result{}, writeErr
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return Result{}, err
		}
	}

	return resultForSum(hasher, size), nil
}

func ResultForBytes(payload []byte) Result {
	sum := md5.Sum(payload)
	return Result{
		ETag: `"` + hex.EncodeToString(sum[:]) + `"`,
		Size: int64(len(payload)),
	}
}

func resultForSum(hasher hash.Hash, size int64) Result {
	return Result{
		ETag: `"` + hex.EncodeToString(hasher.Sum(nil)) + `"`,
		Size: size,
	}
}
