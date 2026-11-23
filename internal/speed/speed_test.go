package speed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAmountScale(t *testing.T) {
	for rate, want := range map[float64]int64{0: step, 50 << 10: step, 512 << 10: 4 * step, 1 << 20: 8 * step,
		100 << 20: 512 * step} {
		if got := amount(rate, 10*time.Second); got != want {
			t.Errorf("10 s at %v B/s = %d, want %d", rate, got, want)
		}
	}
}

func measure(m *Meter) {
	at := time.Unix(0, 0)
	m.Arrive(0, true, at)
	for i := 1; i <= 32; i++ {
		m.Arrive(96<<10, false, at.Add(time.Duration(i)*time.Second/32))
	}
	m.Leave(at.Add(time.Second))
}

func TestAmountAddsTheLatencyToTheTransferTime(t *testing.T) {
	var m Meter
	if got := m.Amount(time.Second); got != step {
		t.Fatalf("before any measurement the amount is %d, want 1 MiB", got)
	}
	measure(&m)
	if r := m.BytesPerSecond(); r != 3<<20 {
		t.Fatalf("the speed is %.0f B/s, want 3 MiB/s", r)
	}

	if got := m.Amount(time.Second); got != 2*step {
		t.Fatalf("without a latency the amount is %d, want 2 MiB", got)
	}

	m.waited(time.Second)
	if got := m.Amount(time.Second); got != 4*step {
		t.Fatalf("with a latency of 1 s the amount is %d, want 4 MiB", got)
	}
}

func TestLatencyFollowsTheSamples(t *testing.T) {
	var m Meter
	m.waited(100 * time.Millisecond)
	if got := m.Latency(); got != 100*time.Millisecond {
		t.Fatalf("the first sample gives %v", got)
	}
	m.waited(200 * time.Millisecond)
	if got := m.Latency(); got != 130*time.Millisecond {
		t.Fatalf("a second sample of 200 ms gives %v, want 130 ms", got)
	}
}

func TestTraceMeasuresTheWaitForTheAnswer(t *testing.T) {
	const wait = 200 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(wait)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	var m Meter
	req, err := http.NewRequestWithContext(m.Trace(context.Background()), http.MethodPost, srv.URL,
		strings.NewReader("small body"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got := m.Latency(); got < wait || got > wait+150*time.Millisecond {
		t.Fatalf("a server that answers after %v measures %v", wait, got)
	}
	if r := m.BytesPerSecond(); r != 0 {
		t.Fatalf("the trace measured a speed of %.0f B/s", r)
	}
}

func TestMeterCountsDownloadsThatShareTheLink(t *testing.T) {
	var m Meter
	at := time.Unix(0, 0)
	ms := func(n int) time.Time { return at.Add(time.Duration(n) * time.Millisecond) }

	m.Arrive(1024, true, ms(300))
	m.Arrive(1024, true, ms(300))
	for i := 1; i <= 50; i++ {
		m.Arrive(1024, false, ms(300+20*i))
		m.Arrive(1024, false, ms(300+20*i))
	}
	m.Leave(ms(1300))
	m.Leave(ms(1300))
	if r := m.BytesPerSecond(); r < 95<<10 || r > 105<<10 {
		t.Fatalf("the link measures %.0f B/s, want 100 KiB/s", r)
	}
}

func TestMeterWaitsForEnoughBytesOnASlowLink(t *testing.T) {
	var m Meter
	at := time.Unix(0, 0)

	m.Arrive(1024, true, at)
	for i := 1; i <= 100; i++ {
		m.Arrive(1024, false, at.Add(time.Duration(i)*50*time.Millisecond))
	}
	if r := m.BytesPerSecond(); r < 19<<10 || r > 21<<10 {
		t.Fatalf("a slow link measures %.0f B/s, want 20 KiB/s", r)
	}
}

func TestMeterAddsUpShortStretchesOfAFastLink(t *testing.T) {
	var m Meter
	at := time.Unix(0, 0)

	for i := range 20 {
		start := at.Add(time.Duration(i) * 50 * time.Millisecond)
		m.Arrive(16<<10, true, start)
		for k := 1; k <= 64; k++ {
			m.Arrive(16<<10, false, start.Add(time.Duration(k)*time.Millisecond/64))
		}
		m.Leave(start.Add(time.Millisecond))
	}
	if r := m.BytesPerSecond(); r < 900<<20 || r > 1100<<20 {
		t.Fatalf("a fast link measures %.0f B/s, want 1 GiB/s", r)
	}
}

func TestAnsweredLeavesTheLatencyOutOfTheSpeed(t *testing.T) {
	var m Meter
	m.waited(250 * time.Millisecond)
	at := time.Unix(0, 0)

	m.Arrive(0, true, at)
	m.Answered(1<<20, at.Add(500*time.Millisecond))
	if r := m.BytesPerSecond(); r != 4<<20 {
		t.Fatalf("the speed is %.0f B/s, want 4 MiB/s", r)
	}
}

func TestAnsweredNeverEndsBeforeTheStretch(t *testing.T) {
	var m Meter
	m.waited(time.Second)
	at := time.Unix(0, 0)

	m.Arrive(0, true, at)
	m.Answered(1<<20, at.Add(500*time.Millisecond))
	if r := m.BytesPerSecond(); r != 0 {
		t.Fatalf("a transfer of no time measured %.0f B/s", r)
	}
	next := at.Add(10 * time.Second)
	m.Arrive(0, true, next)
	m.Answered(1<<20, next.Add(1500*time.Millisecond))

	if r := m.BytesPerSecond(); r != 2<<20 {
		t.Fatalf("the speed is %.0f B/s, want 2 MiB/s", r)
	}
}

func TestAmountCountsWithTheLimitWhenTheLinkMeasuresFaster(t *testing.T) {
	var m Meter
	measure(&m)

	m.SetLimit(8 << 20)
	if got := m.Amount(time.Second); got != 2*step {
		t.Fatalf("with a limit above the speed the amount is %d, want 2 MiB", got)
	}

	m.SetLimit(1 << 20)
	if got := m.Amount(2 * time.Second); got != 2*step {
		t.Fatalf("held to 1 MiB/s the amount of 2 s is %d, want 2 MiB", got)
	}
	m.SetLimit(0)
	if got := m.Amount(2 * time.Second); got != 4*step {
		t.Fatalf("with the limit lifted the amount of 2 s is %d, want 4 MiB", got)
	}
}

func TestShareDividesTheLinkAmongTheTransfersUnderWay(t *testing.T) {
	var m Meter
	measure(&m)
	if alone, whole := m.Share(10*time.Second), m.Amount(10*time.Second); alone != whole {
		t.Fatalf("with no transfer under way the share is %d, the whole link %d", alone, whole)
	}
	at := time.Unix(100, 0)
	for range 3 {
		m.Arrive(0, true, at)
	}
	if got, want := m.Share(10*time.Second), amount(3<<20/4, 10*time.Second); got != want {
		t.Fatalf("beside three transfers the share is %d, want %d, a quarter of the link", got, want)
	}
}
