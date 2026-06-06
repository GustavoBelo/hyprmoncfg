package hypr

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

const (
	drmPropNameLen    = 32
	drmDisplayModeLen = 32
	drmIoctlBase      = uintptr('d')
	drmIoctlRead      = uintptr(2)
	drmIoctlWrite     = uintptr(1)
	drmIoctlGetConnNr = uintptr(0xA7)
	drmIoctlGetPropNr = uintptr(0xAA)
	drmIoctlGetBlobNr = uintptr(0xAC)
	defaultDRMSysRoot = "/sys/class/drm"
	defaultDRMDevRoot = "/dev/dri"
	connectorPathProp = "PATH"
)

var (
	drmIoctlGetConnector = ioctlIOWR(drmIoctlBase, drmIoctlGetConnNr, unsafe.Sizeof(drmModeGetConnector{}))
	drmIoctlGetProperty  = ioctlIOWR(drmIoctlBase, drmIoctlGetPropNr, unsafe.Sizeof(drmModeGetProperty{}))
	drmIoctlGetPropBlob  = ioctlIOWR(drmIoctlBase, drmIoctlGetBlobNr, unsafe.Sizeof(drmModeGetBlob{}))
)

type drmModeModeInfo struct {
	Clock      uint32
	HDisplay   uint16
	HSyncStart uint16
	HSyncEnd   uint16
	HTotal     uint16
	HSkew      uint16
	VDisplay   uint16
	VSyncStart uint16
	VSyncEnd   uint16
	VTotal     uint16
	VScan      uint16
	VRefresh   uint32
	Flags      uint32
	Type       uint32
	Name       [drmDisplayModeLen]byte
}

type drmModeGetConnector struct {
	EncodersPtr     uint64
	ModesPtr        uint64
	PropsPtr        uint64
	PropValuesPtr   uint64
	CountModes      uint32
	CountProps      uint32
	CountEncoders   uint32
	EncoderID       uint32
	ConnectorID     uint32
	ConnectorType   uint32
	ConnectorTypeID uint32
	Connection      uint32
	MMWidth         uint32
	MMHeight        uint32
	Subpixel        uint32
	Pad             uint32
}

type drmModeGetProperty struct {
	ValuesPtr      uint64
	EnumBlobPtr    uint64
	PropID         uint32
	Flags          uint32
	Name           [drmPropNameLen]byte
	CountValues    uint32
	CountEnumBlobs uint32
}

type drmModeGetBlob struct {
	BlobID uint32
	Length uint32
	Data   uint64
}

type drmConnectorEntry struct {
	card        string
	name        string
	connectorID uint32
}

func enrichMonitorConnectorPaths(monitors []Monitor) {
	wanted := make(map[string]bool, len(monitors))
	for _, monitor := range monitors {
		if strings.TrimSpace(monitor.Name) != "" {
			wanted[monitor.Name] = true
		}
	}
	if len(wanted) == 0 {
		return
	}

	paths := drmConnectorPathsForNames(defaultDRMSysRoot, defaultDRMDevRoot, wanted)
	for i := range monitors {
		if path := paths[monitors[i].Name]; path != "" {
			monitors[i].ConnectorPath = path
		}
	}
}

func drmConnectorPathsForNames(sysRoot string, devRoot string, wanted map[string]bool) map[string]string {
	entries := drmConnectorEntries(sysRoot, wanted)
	if len(entries) == 0 {
		return nil
	}

	byCard := make(map[string][]drmConnectorEntry, len(entries))
	for _, entry := range entries {
		byCard[entry.card] = append(byCard[entry.card], entry)
	}

	paths := make(map[string]string, len(entries))
	for card, cardEntries := range byCard {
		devicePath := filepath.Join(devRoot, card)
		file, err := os.Open(devicePath)
		if err != nil {
			continue
		}
		for _, entry := range cardEntries {
			path, err := drmConnectorPath(file.Fd(), entry.connectorID)
			if err == nil && path != "" {
				paths[entry.name] = path
			}
		}
		_ = file.Close()
	}
	return paths
}

func drmConnectorEntries(sysRoot string, wanted map[string]bool) []drmConnectorEntry {
	matches, err := filepath.Glob(filepath.Join(sysRoot, "card*-*"))
	if err != nil {
		return nil
	}

	entries := make([]drmConnectorEntry, 0, len(matches))
	for _, path := range matches {
		base := filepath.Base(path)
		card, name, ok := strings.Cut(base, "-")
		if !ok || card == "" || name == "" {
			continue
		}
		if len(wanted) > 0 && !wanted[name] {
			continue
		}

		data, err := os.ReadFile(filepath.Join(path, "connector_id"))
		if err != nil {
			continue
		}
		id, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 32)
		if err != nil || id == 0 {
			continue
		}
		entries = append(entries, drmConnectorEntry{
			card:        card,
			name:        name,
			connectorID: uint32(id),
		})
	}
	return entries
}

func drmConnectorPath(fd uintptr, connectorID uint32) (string, error) {
	connector, props, values, err := drmConnectorProperties(fd, connectorID)
	if err != nil {
		return "", err
	}
	if connector.CountProps == 0 {
		return "", nil
	}

	for idx, propID := range props {
		name, err := drmPropertyName(fd, propID)
		if err != nil || name != connectorPathProp || idx >= len(values) || values[idx] == 0 {
			continue
		}
		return drmBlobString(fd, uint32(values[idx]))
	}
	return "", nil
}

func drmConnectorProperties(fd uintptr, connectorID uint32) (drmModeGetConnector, []uint32, []uint64, error) {
	var firstMode drmModeModeInfo
	first := drmModeGetConnector{
		ConnectorID: connectorID,
		CountModes:  1,
		ModesPtr:    uint64(uintptr(unsafe.Pointer(&firstMode))),
	}
	if err := ioctl(fd, drmIoctlGetConnector, unsafe.Pointer(&first)); err != nil {
		return drmModeGetConnector{}, nil, nil, err
	}
	if first.CountProps == 0 {
		return first, nil, nil, nil
	}

	modeCount := first.CountModes
	if modeCount == 0 {
		modeCount = 1
	}
	encoderCount := first.CountEncoders

	modes := make([]drmModeModeInfo, modeCount)
	encoders := make([]uint32, encoderCount)
	props := make([]uint32, first.CountProps)
	values := make([]uint64, first.CountProps)

	for attempts := 0; attempts < 3; attempts++ {
		connector := drmModeGetConnector{
			ConnectorID:   connectorID,
			CountModes:    uint32(len(modes)),
			CountProps:    uint32(len(props)),
			CountEncoders: uint32(len(encoders)),
			ModesPtr:      slicePtr64(modes),
			PropsPtr:      slicePtr64(props),
			PropValuesPtr: slicePtr64(values),
			EncodersPtr:   slicePtr64(encoders),
		}
		if err := ioctl(fd, drmIoctlGetConnector, unsafe.Pointer(&connector)); err != nil {
			return drmModeGetConnector{}, nil, nil, err
		}
		if int(connector.CountProps) == len(props) &&
			int(connector.CountModes) == len(modes) &&
			int(connector.CountEncoders) == len(encoders) {
			return connector, props[:connector.CountProps], values[:connector.CountProps], nil
		}
		if int(connector.CountModes) != len(modes) {
			modes = make([]drmModeModeInfo, connector.CountModes)
		}
		if int(connector.CountEncoders) != len(encoders) {
			encoders = make([]uint32, connector.CountEncoders)
		}
		if int(connector.CountProps) != len(props) {
			props = make([]uint32, connector.CountProps)
			values = make([]uint64, connector.CountProps)
		}
	}

	return drmModeGetConnector{}, nil, nil, syscall.EAGAIN
}

func drmPropertyName(fd uintptr, propID uint32) (string, error) {
	property := drmModeGetProperty{PropID: propID}
	if err := ioctl(fd, drmIoctlGetProperty, unsafe.Pointer(&property)); err != nil {
		return "", err
	}
	return cString(property.Name[:]), nil
}

func drmBlobString(fd uintptr, blobID uint32) (string, error) {
	blob := drmModeGetBlob{BlobID: blobID}
	if err := ioctl(fd, drmIoctlGetPropBlob, unsafe.Pointer(&blob)); err != nil {
		return "", err
	}
	if blob.Length == 0 {
		return "", nil
	}

	data := make([]byte, blob.Length)
	blob.Data = uint64(uintptr(unsafe.Pointer(&data[0])))
	if err := ioctl(fd, drmIoctlGetPropBlob, unsafe.Pointer(&blob)); err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\x00"), nil
}

func slicePtr64[T any](values []T) uint64 {
	if len(values) == 0 {
		return 0
	}
	return uint64(uintptr(unsafe.Pointer(&values[0])))
}

func cString(value []byte) string {
	for idx, b := range value {
		if b == 0 {
			return string(value[:idx])
		}
	}
	return string(value)
}

func ioctlIOWR(base uintptr, nr uintptr, size uintptr) uintptr {
	return ioctlRequest(drmIoctlRead|drmIoctlWrite, base, nr, size)
}

func ioctlRequest(dir uintptr, typ uintptr, nr uintptr, size uintptr) uintptr {
	return (dir << 30) | (size << 16) | (typ << 8) | nr
}

func ioctl(fd uintptr, request uintptr, arg unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(arg))
	if errno != 0 {
		return errno
	}
	return nil
}
