package backupds

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"sync"

	logging "github.com/ipfs/go-log/v2"
	cbg "github.com/whyrusleeping/cbor-gen"

	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
)

var log = logging.Logger("backupds")

type Datastore struct {
	child datastore.Batching

	backupLk sync.RWMutex
}

func Wrap(child datastore.Batching) *Datastore {
	return &Datastore{
		child: child,
	}
}

// Writes a datastore dump into the provided writer as
// [array(*) of [key, value] tuples, checksum]
func (d *Datastore) Backup(ctx context.Context, out io.Writer) error {
	scratch := make([]byte, 9)

	if err := cbg.WriteMajorTypeHeaderBuf(scratch, out, cbg.MajArray, 2); err != nil {
		return fmt.Errorf("writing tuple header: %w", err)
	}

	hasher := sha256.New()
	hout := io.MultiWriter(hasher, out)

	// write KVs
	{
		// write indefinite length array header
		if _, err := hout.Write([]byte{0x9f}); err != nil {
			return fmt.Errorf("writing header: %w", err)
		}

		d.backupLk.Lock()
		defer d.backupLk.Unlock()

		log.Info("Starting datastore backup")
		defer log.Info("Datastore backup done")

		qr, err := d.child.Query(ctx, query.Query{})
		if err != nil {
			return fmt.Errorf("query: %w", err)
		}
		defer func() {
			if err := qr.Close(); err != nil {
				log.Errorf("query close error: %+v", err)
				return
			}
		}()

		for result := range qr.Next() {
			if err := cbg.WriteMajorTypeHeaderBuf(scratch, hout, cbg.MajArray, 2); err != nil {
				return fmt.Errorf("writing tuple header: %w", err)
			}

			if err := cbg.WriteMajorTypeHeaderBuf(scratch, hout, cbg.MajByteString, uint64(len([]byte(result.Key)))); err != nil {
				return fmt.Errorf("writing key header: %w", err)
			}

			if _, err := hout.Write([]byte(result.Key)[:]); err != nil {
				return fmt.Errorf("writing key: %w", err)
			}

			if err := cbg.WriteMajorTypeHeaderBuf(scratch, hout, cbg.MajByteString, uint64(len(result.Value))); err != nil {
				return fmt.Errorf("writing value header: %w", err)
			}

			if _, err := hout.Write(result.Value[:]); err != nil {
				return fmt.Errorf("writing value: %w", err)
			}
		}

		// array break
		if _, err := hout.Write([]byte{0xff}); err != nil {
			return fmt.Errorf("writing array 'break': %w", err)
		}
	}

	// Write the checksum
	{
		sum := hasher.Sum(nil)

		if err := cbg.WriteMajorTypeHeaderBuf(scratch, hout, cbg.MajByteString, uint64(len(sum))); err != nil {
			return fmt.Errorf("writing checksum header: %w", err)
		}

		if _, err := hout.Write(sum[:]); err != nil {
			return fmt.Errorf("writing checksum: %w", err)
		}
	}

	return nil
}

// proxy

func (d *Datastore) Get(ctx context.Context, key datastore.Key) (value []byte, err error) {
	return d.child.Get(ctx, key)
}

func (d *Datastore) Has(ctx context.Context, key datastore.Key) (exists bool, err error) {
	return d.child.Has(ctx, key)
}

func (d *Datastore) GetSize(ctx context.Context, key datastore.Key) (size int, err error) {
	return d.child.GetSize(ctx, key)
}

func (d *Datastore) Query(ctx context.Context, q query.Query) (query.Results, error) {
	return d.child.Query(ctx, q)
}

func (d *Datastore) Put(ctx context.Context, key datastore.Key, value []byte) error {
	d.backupLk.RLock()
	defer d.backupLk.RUnlock()

	return d.child.Put(ctx, key, value)
}

func (d *Datastore) Delete(ctx context.Context, key datastore.Key) error {
	d.backupLk.RLock()
	defer d.backupLk.RUnlock()

	return d.child.Delete(ctx, key)
}

func (d *Datastore) Sync(ctx context.Context, prefix datastore.Key) error {
	d.backupLk.RLock()
	defer d.backupLk.RUnlock()

	return d.child.Sync(ctx, prefix)
}

func (d *Datastore) Close() error {
	d.backupLk.RLock()
	defer d.backupLk.RUnlock()

	return d.child.Close()
}

func (d *Datastore) Batch(ctx context.Context) (datastore.Batch, error) {
	b, err := d.child.Batch(ctx)
	if err != nil {
		return nil, err
	}

	return &bbatch{
		b:   b,
		rlk: d.backupLk.RLocker(),
	}, nil
}

type bbatch struct {
	b   datastore.Batch
	rlk sync.Locker
}

func (b *bbatch) Put(ctx context.Context, key datastore.Key, value []byte) error {
	return b.b.Put(ctx, key, value)
}

func (b *bbatch) Delete(ctx context.Context, key datastore.Key) error {
	return b.b.Delete(ctx, key)
}

func (b *bbatch) Commit(ctx context.Context) error {
	b.rlk.Lock()
	defer b.rlk.Unlock()

	return b.b.Commit(ctx)
}

var _ datastore.Batch = &bbatch{}
var _ datastore.Batching = &Datastore{}
