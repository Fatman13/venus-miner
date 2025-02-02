package backupds

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"

	"github.com/ipfs/go-datastore"
	cbg "github.com/whyrusleeping/cbor-gen"
)

func ReadBackup(r io.Reader, cb func(key datastore.Key, value []byte) error) error {
	scratch := make([]byte, 9)

	if _, err := r.Read(scratch[:1]); err != nil {
		return fmt.Errorf("reading array header: %w", err)
	}

	if scratch[0] != 0x82 {
		return fmt.Errorf("expected array(2) header byte 0x82, got %x", scratch[0])
	}

	hasher := sha256.New()
	hr := io.TeeReader(r, hasher)

	if _, err := hr.Read(scratch[:1]); err != nil {
		return fmt.Errorf("reading array header: %w", err)
	}

	if scratch[0] != 0x9f {
		return fmt.Errorf("expected indefinite length array header byte 0x9f, got %x", scratch[0])
	}

	for {
		if _, err := hr.Read(scratch[:1]); err != nil {
			return fmt.Errorf("reading tuple header: %w", err)
		}

		if scratch[0] == 0xff {
			break
		}

		if scratch[0] != 0x82 {
			return fmt.Errorf("expected array(2) header 0x82, got %x", scratch[0])
		}

		keyb, err := cbg.ReadByteArray(hr, 1<<40)
		if err != nil {
			return fmt.Errorf("reading key: %w", err)
		}
		key := datastore.NewKey(string(keyb))

		value, err := cbg.ReadByteArray(hr, 1<<40)
		if err != nil {
			return fmt.Errorf("reading value: %w", err)
		}

		if err := cb(key, value); err != nil {
			return err
		}
	}

	sum := hasher.Sum(nil)

	expSum, err := cbg.ReadByteArray(r, 32)
	if err != nil {
		return fmt.Errorf("reading expected checksum: %w", err)
	}

	if !bytes.Equal(sum, expSum) {
		return fmt.Errorf("checksum didn't match; expected %x, got %x", expSum, sum)
	}

	return nil
}
