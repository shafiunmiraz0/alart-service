package monitor

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// SystemMetrics holds a snapshot of all system resource metrics.
type SystemMetrics struct {
	Timestamp time.Time

	// CPU
	CPUPercent float64 // Overall CPU usage percentage (0-100)

	// Memory
	RAMTotal     uint64  // Total RAM in bytes
	RAMUsed      uint64  // Used RAM in bytes
	RAMPercent   float64 // RAM usage percentage (0-100)

	// Disk partitions
	Disks []DiskMetric

	// Disk I/O
	DiskIOReadBytes  uint64  // Total bytes read since last sample
	DiskIOWriteBytes uint64  // Total bytes written since last sample
	DiskIOReadMBps   float64 // Read rate in MB/s
	DiskIOWriteMBps  float64 // Write rate in MB/s

	// Network
	NetRxBytes uint64  // Total bytes received since last sample
	NetTxBytes uint64  // Total bytes transmitted since last sample
	NetRxMBps  float64 // Receive rate in MB/s
	NetTxMBps  float64 // Transmit rate in MB/s
}

// DiskMetric holds usage info for a single disk partition.
type DiskMetric struct {
	MountPoint string
	Device     string
	Total      uint64
	Used       uint64
	Percent    float64
}

// Collector gathers system metrics from /proc and /sys on Linux.
type Collector struct {
	// Previous samples for rate calculation.
	prevCPU      cpuSample
	prevDiskIO   diskIOSample
	prevNet      netSample
	prevTime     time.Time
	initialized  bool
}

type cpuSample struct {
	user, nice, system, idle, iowait, irq, softirq, steal uint64
}

type diskIOSample struct {
	readBytes  uint64
	writeBytes uint64
}

type netSample struct {
	rxBytes uint64
	txBytes uint64
}

// NewCollector creates a new system metrics collector.
func NewCollector() *Collector {
	return &Collector{}
}

// Collect gathers a full snapshot of system metrics.
func (c *Collector) Collect() (*SystemMetrics, error) {
	now := time.Now()
	m := &SystemMetrics{Timestamp: now}

	// --- CPU ---
	cpuNow, err := readCPUSample()
	if err != nil {
		return nil, fmt.Errorf("cpu: %w", err)
	}
	if c.initialized {
		m.CPUPercent = calcCPUPercent(c.prevCPU, cpuNow)
	}
	c.prevCPU = cpuNow

	// --- Memory ---
	if err := readMemInfo(m); err != nil {
		return nil, fmt.Errorf("memory: %w", err)
	}

	// --- Disk usage ---
	disks, err := readDiskUsage()
	if err != nil {
		return nil, fmt.Errorf("disk: %w", err)
	}
	m.Disks = disks

	// --- Disk I/O ---
	diskIO, err := readDiskIO()
	if err != nil {
		return nil, fmt.Errorf("diskio: %w", err)
	}
	if c.initialized {
		elapsed := now.Sub(c.prevTime).Seconds()
		if elapsed > 0 {
			readDelta := diskIO.readBytes - c.prevDiskIO.readBytes
			writeDelta := diskIO.writeBytes - c.prevDiskIO.writeBytes
			m.DiskIOReadBytes = readDelta
			m.DiskIOWriteBytes = writeDelta
			m.DiskIOReadMBps = float64(readDelta) / (1024 * 1024) / elapsed
			m.DiskIOWriteMBps = float64(writeDelta) / (1024 * 1024) / elapsed
		}
	}
	c.prevDiskIO = diskIO

	// --- Network ---
	netNow, err := readNetStats()
	if err != nil {
		return nil, fmt.Errorf("network: %w", err)
	}
	if c.initialized {
		elapsed := now.Sub(c.prevTime).Seconds()
		if elapsed > 0 {
			rxDelta := netNow.rxBytes - c.prevNet.rxBytes
			txDelta := netNow.txBytes - c.prevNet.txBytes
			m.NetRxBytes = rxDelta
			m.NetTxBytes = txDelta
			m.NetRxMBps = float64(rxDelta) / (1024 * 1024) / elapsed
			m.NetTxMBps = float64(txDelta) / (1024 * 1024) / elapsed
		}
	}
	c.prevNet = netNow

	c.prevTime = now
	c.initialized = true

	return m, nil
}

// readCPUSample reads /proc/stat and returns the aggregate CPU times.
func readCPUSample() (cpuSample, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuSample{}, err
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line)
			if len(fields) < 8 {
				return cpuSample{}, fmt.Errorf("unexpected /proc/stat cpu line format")
			}
			var s cpuSample
			s.user, _ = strconv.ParseUint(fields[1], 10, 64)
			s.nice, _ = strconv.ParseUint(fields[2], 10, 64)
			s.system, _ = strconv.ParseUint(fields[3], 10, 64)
			s.idle, _ = strconv.ParseUint(fields[4], 10, 64)
			s.iowait, _ = strconv.ParseUint(fields[5], 10, 64)
			s.irq, _ = strconv.ParseUint(fields[6], 10, 64)
			s.softirq, _ = strconv.ParseUint(fields[7], 10, 64)
			if len(fields) > 8 {
				s.steal, _ = strconv.ParseUint(fields[8], 10, 64)
			}
			return s, nil
		}
	}
	return cpuSample{}, fmt.Errorf("/proc/stat: cpu line not found")
}

// calcCPUPercent computes CPU usage percentage between two samples.
func calcCPUPercent(prev, curr cpuSample) float64 {
	prevTotal := prev.user + prev.nice + prev.system + prev.idle + prev.iowait + prev.irq + prev.softirq + prev.steal
	currTotal := curr.user + curr.nice + curr.system + curr.idle + curr.iowait + curr.irq + curr.softirq + curr.steal

	totalDelta := float64(currTotal - prevTotal)
	if totalDelta == 0 {
		return 0
	}

	idleDelta := float64((curr.idle + curr.iowait) - (prev.idle + prev.iowait))
	return ((totalDelta - idleDelta) / totalDelta) * 100.0
}

// readMemInfo reads /proc/meminfo and populates RAM fields.
func readMemInfo(m *SystemMetrics) error {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return err
	}

	values := make(map[string]uint64)
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		valStr := strings.TrimSpace(parts[1])
		valStr = strings.TrimSuffix(valStr, " kB")
		valStr = strings.TrimSpace(valStr)
		val, _ := strconv.ParseUint(valStr, 10, 64)
		values[key] = val
	}

	memTotal := values["MemTotal"]
	memFree := values["MemFree"]
	buffers := values["Buffers"]
	cached := values["Cached"]
	sReclaimable := values["SReclaimable"]

	// Effective used memory = Total - Free - Buffers - Cached - SReclaimable
	memUsed := memTotal - memFree - buffers - cached - sReclaimable

	m.RAMTotal = memTotal * 1024 // Convert from kB to bytes
	m.RAMUsed = memUsed * 1024
	if memTotal > 0 {
		m.RAMPercent = (float64(memUsed) / float64(memTotal)) * 100.0
	}

	return nil
}

// readDiskUsage reads /proc/mounts and uses syscall.Statfs to get usage.
func readDiskUsage() ([]DiskMetric, error) {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var disks []DiskMetric

	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		device := fields[0]
		mountPoint := fields[1]

		// Only consider real disk devices.
		if !strings.HasPrefix(device, "/dev/") {
			continue
		}

		// Skip duplicates.
		if seen[mountPoint] {
			continue
		}
		seen[mountPoint] = true

		total, used, percent, err := statFS(mountPoint)
		if err != nil {
			continue // Skip partitions we can't stat.
		}

		disks = append(disks, DiskMetric{
			MountPoint: mountPoint,
			Device:     device,
			Total:      total,
			Used:       used,
			Percent:    percent,
		})
	}

	return disks, nil
}

// readDiskIO reads /proc/diskstats and aggregates I/O counters.
func readDiskIO() (diskIOSample, error) {
	data, err := os.ReadFile("/proc/diskstats")
	if err != nil {
		return diskIOSample{}, err
	}

	var totalRead, totalWrite uint64

	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 14 {
			continue
		}
		devName := fields[2]

		// Only count whole disks (sd*, nvme*n*, vd*), skip partitions.
		if !isWholeDisk(devName) {
			continue
		}

		// Field index 5 = sectors read, field index 9 = sectors written
		// (0-indexed from the diskstats line, which starts at major, minor, name...)
		sectorsRead, _ := strconv.ParseUint(fields[5], 10, 64)
		sectorsWritten, _ := strconv.ParseUint(fields[9], 10, 64)

		// Sector size is typically 512 bytes.
		totalRead += sectorsRead * 512
		totalWrite += sectorsWritten * 512
	}

	return diskIOSample{
		readBytes:  totalRead,
		writeBytes: totalWrite,
	}, nil
}

// isWholeDisk returns true if the device name looks like a whole disk
// (e.g. sda, nvme0n1, vda) rather than a partition (e.g. sda1, nvme0n1p1).
func isWholeDisk(name string) bool {
	// sd* disks: "sda" (no trailing digit for whole disk)
	if strings.HasPrefix(name, "sd") && len(name) == 3 {
		return true
	}
	// vd* disks: "vda"
	if strings.HasPrefix(name, "vd") && len(name) == 3 {
		return true
	}
	// nvme disks: "nvme0n1" (no trailing 'p' + digit)
	if strings.HasPrefix(name, "nvme") && !strings.Contains(name, "p") {
		// Actually nvme0n1 contains no partition indicator 'p'
		// nvme0n1p1 would be a partition
		return true
	}
	// Handle nvme with 'n' but check for partition 'p' after 'n'
	if strings.HasPrefix(name, "nvme") {
		// nvme0n1 = disk, nvme0n1p1 = partition
		nIdx := strings.LastIndex(name, "n")
		if nIdx >= 0 {
			after := name[nIdx+1:]
			return !strings.Contains(after, "p")
		}
	}
	return false
}

// readNetStats reads /proc/net/dev and aggregates network counters.
func readNetStats() (netSample, error) {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return netSample{}, err
	}

	var totalRx, totalTx uint64

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		iface := strings.TrimSpace(parts[0])

		// Skip loopback.
		if iface == "lo" {
			continue
		}

		fields := strings.Fields(parts[1])
		if len(fields) < 9 {
			continue
		}

		rxBytes, _ := strconv.ParseUint(fields[0], 10, 64)
		txBytes, _ := strconv.ParseUint(fields[8], 10, 64)

		totalRx += rxBytes
		totalTx += txBytes
	}

	return netSample{
		rxBytes: totalRx,
		txBytes: totalTx,
	}, nil
}
