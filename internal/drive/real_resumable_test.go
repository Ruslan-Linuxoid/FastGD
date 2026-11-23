//go:build realdrive

package drive_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

const quantum = 256 << 10

func TestRealCutChunkOfAResumableSession(t *testing.T) {
	r := testenv.RealDrive(t)
	const size = 24 << 20
	content := pattern(size)
	ids, err := r.Drive.GenerateIDs(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	session, err := r.Drive.StartCreate(t.Context(), ids[0], "resumable.bin", r.Folder, "application/octet-stream",
		time.Now(), size)
	if err != nil {
		t.Fatal(err)
	}
	const chunk = 8 << 20
	p, err := r.Drive.SendChunk(t.Context(), session, 0, chunk, size, bytes.NewReader(content[:chunk]))
	if err != nil || p.Confirmed != chunk {
		t.Fatalf("the first chunk: confirmed %d, %v", p.Confirmed, err)
	}

	cut := &cutAfter{r: bytes.NewReader(content[chunk : 2*chunk]), n: 5<<20 + 300<<10}
	_, err = r.Drive.SendChunk(t.Context(), session, chunk, chunk, size, cut)
	t.Logf("the cut chunk ends with %v", err)
	p, err = r.Drive.UploadStatus(t.Context(), session, size)
	if err != nil {
		t.Fatal(err)
	}
	confirmed := p.Confirmed
	t.Logf("after the cut chunk Drive confirms %d bytes: %d past the first chunk, a multiple of 256 KiB from the start %v",
		confirmed, confirmed-chunk, confirmed%quantum == 0)

	end := (confirmed/quantum + 2) * quantum
	p, err = r.Drive.SendChunk(t.Context(), session, confirmed, end-confirmed, size,
		bytes.NewReader(content[confirmed:end]))
	t.Logf("a chunk of %d bytes, a multiple of 256 KiB %v, ending at %d: confirmed %d, %v",
		end-confirmed, (end-confirmed)%quantum == 0, end, p.Confirmed, err)
	if err != nil {
		t.Fatal(err)
	}
	at := p.Confirmed
	n := int64(2 * quantum)
	p, err = r.Drive.SendChunk(t.Context(), session, at, n, size, bytes.NewReader(content[at:at+n]))
	t.Logf("a chunk of 512 KiB from %d: confirmed %d, %v", at, p.Confirmed, err)
	if err != nil {
		t.Fatal(err)
	}
	at = p.Confirmed
	p, err = r.Drive.SendChunk(t.Context(), session, at, size-at, size, bytes.NewReader(content[at:]))
	if err != nil || p.Done == nil {
		t.Fatalf("the last chunk from %d: %+v, %v", at, p, err)
	}
	requireContent(t, r, p.Done, content)
}

func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i * 31)
	}
	return b
}

func requireContent(t *testing.T, r *testenv.Real, f *drive.File, content []byte) {
	t.Helper()
	if f.Size != int64(len(content)) {
		t.Fatalf("Drive holds %d bytes of the file, want %d", f.Size, len(content))
	}
	var got []byte
	if err := r.Drive.Stream(t.Context(), f.ID, 0, f.Size, func(b []byte) error {
		got = append(got, b...)
		return nil
	}); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("the file in Drive is the content: %v, %v", bytes.Equal(got, content), err)
	}
}
