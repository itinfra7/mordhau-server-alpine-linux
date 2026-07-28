package manager

import (
	"bufio"
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type cpuSample struct {
	total uint64
	idle  uint64
	valid bool
}

const metricsSampleInterval = time.Minute

func readCPUSample() (cpuSample, error) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return cpuSample{}, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return cpuSample{}, errors.New("missing aggregate CPU line")
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuSample{}, errors.New("invalid aggregate CPU line")
	}
	var values []uint64
	for _, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return cpuSample{}, err
		}
		values = append(values, value)
	}
	var total uint64
	for _, value := range values {
		total += value
	}
	idle := values[3]
	if len(values) > 4 {
		idle += values[4]
	}
	return cpuSample{total: total, idle: idle, valid: true}, nil
}

func readMeminfo() (memory Usage, swap Usage, err error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return Usage{}, Usage{}, err
	}
	defer file.Close()
	values := make(map[string]uint64)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		value, parseErr := strconv.ParseUint(fields[1], 10, 64)
		if parseErr != nil {
			continue
		}
		values[key] = value * 1024
	}
	if err := scanner.Err(); err != nil {
		return Usage{}, Usage{}, err
	}
	memory.Total = values["MemTotal"]
	available := values["MemAvailable"]
	if available > memory.Total {
		available = memory.Total
	}
	memory.Free = available
	memory.Used = memory.Total - available
	memory.Pct = percent(memory.Used, memory.Total)

	swap.Total = values["SwapTotal"]
	swap.Free = values["SwapFree"]
	if swap.Free > swap.Total {
		swap.Free = swap.Total
	}
	swap.Used = swap.Total - swap.Free
	swap.Pct = percent(swap.Used, swap.Total)
	return memory, swap, nil
}

func readDiskUsage(path string) (Usage, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return Usage{}, err
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	if free > total {
		free = total
	}
	used := total - free
	return Usage{Total: total, Used: used, Free: free, Pct: percent(used, total)}, nil
}

func percent(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return math.Round((float64(used)/float64(total))*1000) / 10
}

func (m *Manager) sampleMetrics() {
	currentCPU, cpuErr := readCPUSample()
	memory, swap, memoryErr := readMeminfo()
	disk, diskErr := readDiskUsage(rootDir)

	m.metricsMu.Lock()
	defer m.metricsMu.Unlock()
	if cpuErr == nil && m.cpu.valid && currentCPU.total > m.cpu.total {
		totalDelta := currentCPU.total - m.cpu.total
		idleDelta := currentCPU.idle - m.cpu.idle
		busy := totalDelta
		if idleDelta <= totalDelta {
			busy -= idleDelta
		}
		m.metrics.CPUPercent = math.Round((float64(busy)/float64(totalDelta))*1000) / 10
	}
	if cpuErr == nil {
		m.cpu = currentCPU
	}
	if memoryErr == nil {
		m.metrics.Memory = memory
		m.metrics.Swap = swap
	}
	if diskErr == nil {
		m.metrics.Disk = disk
	}
	m.metrics.SampledAt = time.Now()
}

func (m *Manager) metricsLoop(ctx context.Context) {
	m.sampleMetrics()
	ticker := time.NewTicker(metricsSampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.sampleMetrics()
		}
	}
}

func serverProcess() (int, bool) {
	if data, err := os.ReadFile(mordhauPIDPath); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && processIsMordhau(pid) {
			return pid, true
		}
	}
	paths, _ := filepath.Glob("/proc/[0-9]*/cmdline")
	for _, path := range paths {
		part := strings.TrimSuffix(strings.TrimPrefix(path, "/proc/"), "/cmdline")
		pid, err := strconv.Atoi(part)
		if err == nil && processIsMordhau(pid) {
			return pid, true
		}
	}
	return 0, false
}

func processIsMordhau(pid int) bool {
	if pid < 2 {
		return false
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return false
	}
	command := strings.ReplaceAll(string(data), "\x00", " ")
	return strings.Contains(command, "MordhauServer.exe") ||
		strings.Contains(command, "MordhauServer-Win64-Shipping.exe")
}

func serverRunning() bool {
	_, running := serverProcess()
	return running
}

func (m *Manager) snapshot() Snapshot {
	m.metricsMu.RLock()
	metrics := m.metrics
	m.metricsMu.RUnlock()

	m.mu.RLock()
	operation := m.op
	m.mu.RUnlock()

	m.rconMu.RLock()
	connected := m.eventSourceConnected
	status := m.eventSourceStatus
	start := 0
	if len(m.rconEvents) > 120 {
		start = len(m.rconEvents) - 120
	}
	events := append([]RCONEvent(nil), m.rconEvents[start:]...)
	currentMap := m.currentMap
	currentGameMode := m.currentGameMode
	m.rconMu.RUnlock()

	pid, running := serverProcess()
	runtime := m.runtimeSummaryView()
	if running && runtime.Ready && runtime.GameModeClass != "" {
		currentGameMode = runtime.GameModeClass
	}
	if !running {
		currentMap = ""
		currentGameMode = ""
	}
	return Snapshot{
		Metrics:              metrics,
		ServerRunning:        running,
		ServerPID:            pid,
		CurrentMap:           currentMap,
		CurrentGameMode:      currentGameMode,
		Language:             m.currentLanguage(),
		Languages:            append([]Language(nil), supportedLanguages...),
		PendingConfig:        pendingConfigExists(),
		Operation:            operation,
		EventSourceConnected: connected,
		EventSourceStatus:    status,
		ServerEvents:         events,
		ModRevision:          m.currentModRevision(),
		PlayerRevision:       m.currentPlayerRevision(),
		RuntimeBridge:        runtime,
		GeneratedAt:          time.Now(),
	}
}
