package spooledtempfile

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// --- helpers ---

type savedPaths struct {
	v2Usage, v2High, v2Max string
	v1Usage, v1Limit       string
	meminfo                string
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func saveAndRedirectPaths(t *testing.T, base string) (restore func()) {
	t.Helper()

	old := savedPaths{
		v2Usage: cgv2UsagePath, v2High: cgv2HighPath, v2Max: cgv2MaxPath,
		v1Usage: cgv1UsagePath, v1Limit: cgv1LimitPath,
		meminfo: procMeminfoPath,
	}

	// Point to files under base; tests will create only what they need.
	cgv2UsagePath = filepath.Join(base, "sys/fs/cgroup/memory.current")
	cgv2HighPath = filepath.Join(base, "sys/fs/cgroup/memory.high")
	cgv2MaxPath = filepath.Join(base, "sys/fs/cgroup/memory.max")
	cgv1UsagePath = filepath.Join(base, "sys/fs/cgroup/memory/memory.usage_in_bytes")
	cgv1LimitPath = filepath.Join(base, "sys/fs/cgroup/memory/memory.limit_in_bytes")
	procMeminfoPath = filepath.Join(base, "proc/meminfo")

	return func() {
		cgv2UsagePath, cgv2HighPath, cgv2MaxPath = old.v2Usage, old.v2High, old.v2Max
		cgv1UsagePath, cgv1LimitPath = old.v1Usage, old.v1Limit
		procMeminfoPath = old.meminfo
	}
}

// --- cgroup v2 ---

func TestCgroupV2_UsesHighWhenStricterThanMax(t *testing.T) {
	dir := t.TempDir()
	restore := saveAndRedirectPaths(t, dir)
	defer restore()

	// usage=400
	writeFile(t, dir, "sys/fs/cgroup/memory.current", "400")
	// high=800, max=1000 -> use high, frac = 0.5
	writeFile(t, dir, "sys/fs/cgroup/memory.high", "800")
	writeFile(t, dir, "sys/fs/cgroup/memory.max", "1000")

	frac, ok, err := cgroupV2UsedFraction()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok")
	}
	if got, want := frac, 0.5; got != want {
		t.Fatalf("frac=%v want=%v", got, want)
	}
}

func TestCgroupV2_FallbackToMaxWhenHighIsMax(t *testing.T) {
	dir := t.TempDir()
	restore := saveAndRedirectPaths(t, dir)
	defer restore()

	writeFile(t, dir, "sys/fs/cgroup/memory.current", "300")
	writeFile(t, dir, "sys/fs/cgroup/memory.high", "max") // unset
	writeFile(t, dir, "sys/fs/cgroup/memory.max", "600")  // use this

	frac, ok, err := cgroupV2UsedFraction()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok")
	}
	if got, want := frac, 0.5; got != want {
		t.Fatalf("frac=%v want=%v", got, want)
	}
}

func TestCgroupV2_FallbackToMaxWhenHighInvalid(t *testing.T) {
	dir := t.TempDir()
	restore := saveAndRedirectPaths(t, dir)
	defer restore()

	writeFile(t, dir, "sys/fs/cgroup/memory.current", "256")
	writeFile(t, dir, "sys/fs/cgroup/memory.high", "not-a-number")
	writeFile(t, dir, "sys/fs/cgroup/memory.max", "512")

	frac, ok, err := cgroupV2UsedFraction()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok")
	}
	if got, want := frac, 0.5; got != want {
		t.Fatalf("frac=%v want=%v", got, want)
	}
}

func TestCgroupV2_UseMaxWhenHighGTE_Max(t *testing.T) {
	dir := t.TempDir()
	restore := saveAndRedirectPaths(t, dir)
	defer restore()

	writeFile(t, dir, "sys/fs/cgroup/memory.current", "900")
	writeFile(t, dir, "sys/fs/cgroup/memory.high", "1000") // >= max
	writeFile(t, dir, "sys/fs/cgroup/memory.max", "1000")

	frac, ok, err := cgroupV2UsedFraction()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok")
	}
	if got, want := frac, 0.9; got != want {
		t.Fatalf("frac=%v want=%v", got, want)
	}
}

func TestCgroupV2_OnlyHighSet(t *testing.T) {
	dir := t.TempDir()
	restore := saveAndRedirectPaths(t, dir)
	defer restore()

	writeFile(t, dir, "sys/fs/cgroup/memory.current", "50")
	writeFile(t, dir, "sys/fs/cgroup/memory.high", "100")
	// no max file

	frac, ok, err := cgroupV2UsedFraction()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok")
	}
	if got, want := frac, 0.5; got != want {
		t.Fatalf("frac=%v want=%v", got, want)
	}
}

func TestCgroupV2_NoLimitsOrUsageFile(t *testing.T) {
	dir := t.TempDir()
	restore := saveAndRedirectPaths(t, dir)
	defer restore()

	// usage missing -> ok=false (not cgroup v2 / not accessible)
	_, ok, err := cgroupV2UsedFraction()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false when usage missing")
	}

	// Now create usage but high=max and no max => no effective limit
	writeFile(t, dir, "sys/fs/cgroup/memory.current", "123")
	writeFile(t, dir, "sys/fs/cgroup/memory.high", "max")
	frac, ok, err := cgroupV2UsedFraction()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false with no effective limit, got ok and frac=%v", frac)
	}
}

// --- cgroup v1 ---

func TestCgroupV1_NormalFraction(t *testing.T) {
	dir := t.TempDir()
	restore := saveAndRedirectPaths(t, dir)
	defer restore()

	writeFile(t, dir, "sys/fs/cgroup/memory/memory.usage_in_bytes", "200")
	writeFile(t, dir, "sys/fs/cgroup/memory/memory.limit_in_bytes", "400")

	frac, ok, err := cgroupV1UsedFraction()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok")
	}
	if got, want := frac, 0.5; got != want {
		t.Fatalf("frac=%v want=%v", got, want)
	}
}

func TestCgroupV1_HugeLimitMeansNoLimit(t *testing.T) {
	dir := t.TempDir()
	restore := saveAndRedirectPaths(t, dir)
	defer restore()

	writeFile(t, dir, "sys/fs/cgroup/memory/memory.usage_in_bytes", "42")
	// > 1<<60
	writeFile(t, dir, "sys/fs/cgroup/memory/memory.limit_in_bytes", strconv.FormatUint((1<<60)+1, 10))

	_, ok, err := cgroupV1UsedFraction()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false for huge limit (no limit)")
	}
}

func TestCgroupV1_MissingFilesOrZeroLimit(t *testing.T) {
	dir := t.TempDir()
	restore := saveAndRedirectPaths(t, dir)
	defer restore()

	// Only usage present -> not ok
	writeFile(t, dir, "sys/fs/cgroup/memory/memory.usage_in_bytes", "7")
	_, ok, err := cgroupV1UsedFraction()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false when limit missing")
	}

	// Now add zero limit -> not ok
	writeFile(t, dir, "sys/fs/cgroup/memory/memory.limit_in_bytes", "0")
	_, ok, err = cgroupV1UsedFraction()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false for zero limit")
	}
}

// --- /proc/meminfo fallback ---

func TestHostMeminfo_UsesMemAvailableWhenPresent(t *testing.T) {
	dir := t.TempDir()
	restore := saveAndRedirectPaths(t, dir)
	defer restore()

	// MemTotal and MemAvailable are in kB
	meminfo := strings.Join([]string{
		"MemTotal:       1000000 kB",
		"MemAvailable:    250000 kB",
		"Buffers:          10000 kB",
		"Cached:           20000 kB",
		"MemFree:          50000 kB",
	}, "\n")
	writeFile(t, dir, "proc/meminfo", meminfo)

	frac, err := hostMeminfoUsedFraction()
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	// used = total - available = 1000000 - 250000 = 750000 => 0.75
	if got, want := frac, 0.75; got != want {
		t.Fatalf("frac=%v want=%v", got, want)
	}
}

func TestHostMeminfo_FallbackWithoutMemAvailable(t *testing.T) {
	dir := t.TempDir()
	restore := saveAndRedirectPaths(t, dir)
	defer restore()

	meminfo := strings.Join([]string{
		"MemTotal:  1000000 kB",
		"MemFree:     100000 kB",
		"Buffers:      50000 kB",
		"Cached:      150000 kB",
	}, "\n")
	writeFile(t, dir, "proc/meminfo", meminfo)

	frac, err := hostMeminfoUsedFraction()
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	// approxAvailable = 100000 + 50000 + 150000 = 300000
	// used = 1000000 - 300000 = 700000 => 0.7
	if got, want := frac, 0.7; got != want {
		t.Fatalf("frac=%v want=%v", got, want)
	}
}

func TestHostMeminfo_Errors(t *testing.T) {
	dir := t.TempDir()
	restore := saveAndRedirectPaths(t, dir)
	defer restore()

	// Missing file
	if _, err := hostMeminfoUsedFraction(); err == nil {
		t.Fatalf("expected error when /proc/meminfo missing")
	}

	// Present but missing MemTotal
	writeFile(t, dir, "proc/meminfo", "MemFree: 1 kB\n")
	if _, err := hostMeminfoUsedFraction(); err == nil {
		t.Fatalf("expected error when MemTotal missing")
	}
}

// --- read helpers ---

func TestReadUint64FileIfExists(t *testing.T) {
	dir := t.TempDir()

	// Missing -> ok=false, err=nil
	if _, ok, err := readUint64FileIfExists(filepath.Join(dir, "nope")); err != nil || ok {
		t.Fatalf("expected ok=false, err=nil for missing file; got ok=%v err=%v", ok, err)
	}

	// Present & valid
	p := writeFile(t, dir, "n.txt", "123\n")
	v, ok, err := readUint64FileIfExists(p)
	if err != nil || !ok || v != 123 {
		t.Fatalf("got v=%d ok=%v err=%v; want 123,true,nil", v, ok, err)
	}

	// Present & invalid -> ok=false, err=nil
	p = writeFile(t, dir, "bad.txt", "not-a-number")
	if _, ok, err := readUint64FileIfExists(p); err != nil || ok {
		t.Fatalf("expected ok=false, err=nil for invalid number; got ok=%v err=%v", ok, err)
	}
}

func TestReadStringFileIfExists(t *testing.T) {
	dir := t.TempDir()

	if _, ok, err := readStringFileIfExists(filepath.Join(dir, "nope")); err != nil || ok {
		t.Fatalf("expected ok=false, err=nil for missing file; got ok=%v err=%v", ok, err)
	}

	p := writeFile(t, dir, "s.txt", " hello \n")
	s, ok, err := readStringFileIfExists(p)
	if err != nil || !ok || strings.TrimSpace(s) != "hello" {
		t.Fatalf("got s=%q ok=%v err=%v; want 'hello',true,nil", s, ok, err)
	}
}
