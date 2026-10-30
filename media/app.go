package media

import (
	"archive/zip"
	"bytes"
	"context"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/lsongdev/files-go/store"
)

// App describes installable packages and executables.
type App struct{ Open Opener }

func (App) Match(f *store.File) bool {
	switch Ext(f.Name) {
	case "apk", "exe", "dll", "msi", "so", "dylib", "elf", "bin":
		return f.Kind == "app"
	}
	return false
}

func (a App) Enrich(ctx context.Context, f *store.File, m *Meta) error {
	input, err := a.Open(ctx, *f)
	if err != nil {
		return err
	}
	defer input.Close()
	m.Kind = "app"
	switch Ext(f.Name) {
	case "apk":
		return withZip(input, func(z *zip.Reader) error { return apk(z, m) })
	case "exe", "dll":
		return portable(input, m)
	case "msi":
		m.Set("Type", "Windows Installer")
		return nil
	}
	if file, err := elf.NewFile(input); err == nil {
		defer file.Close()
		m.Set("Format", fmt.Sprintf("ELF %s %s", strings.TrimPrefix(file.Class.String(), "ELFCLASS"), strings.TrimPrefix(file.Type.String(), "ET_")))
		m.Set("Architecture", strings.TrimPrefix(file.Machine.String(), "EM_"))
		return nil
	}
	if file, err := macho.NewFile(input); err == nil {
		defer file.Close()
		m.Set("Format", "Mach-O "+file.Type.String())
		m.Set("Architecture", file.Cpu.String())
		return nil
	}
	if file, err := macho.NewFatFile(input); err == nil {
		defer file.Close()
		var arches []string
		for _, arch := range file.Arches {
			arches = append(arches, arch.Cpu.String())
		}
		m.Set("Format", "Mach-O universal")
		m.Set("Architecture", strings.Join(arches, ", "))
	}
	return nil
}

var peVersionKeys = []string{"ProductName", "FileDescription", "CompanyName", "ProductVersion", "FileVersion", "LegalCopyright"}

func portable(input *os.File, m *Meta) error {
	file, err := pe.NewFile(input)
	if err != nil {
		if legacyErr := legacyWindows(input, m); legacyErr == nil {
			return nil
		}
		return fmt.Errorf("not a PE file: %w", err)
	}
	defer file.Close()
	arch := map[uint16]string{pe.IMAGE_FILE_MACHINE_I386: "x86", pe.IMAGE_FILE_MACHINE_AMD64: "x86-64", pe.IMAGE_FILE_MACHINE_ARM64: "ARM64", pe.IMAGE_FILE_MACHINE_ARMNT: "ARM"}[file.Machine]
	m.Set("Architecture", arch)
	kind := "Windows executable"
	if file.Characteristics&pe.IMAGE_FILE_DLL != 0 {
		kind = "Windows library"
	}
	var subsystem uint16
	switch h := file.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		subsystem = h.Subsystem
	case *pe.OptionalHeader64:
		subsystem = h.Subsystem
	}
	if subsystem == pe.IMAGE_SUBSYSTEM_WINDOWS_CUI {
		kind += " (console)"
	}
	m.Set("Type", kind)
	section := file.Section(".rsrc")
	if section == nil || section.Size > 32<<20 {
		return nil
	}
	data, err := section.Data()
	if err != nil {
		return nil
	}
	// VS_VERSIONINFO string tables: a UTF-16 key, padding to 4 bytes, value.
	for _, key := range peVersionKeys {
		needle := utf16le(key + "\x00")
		i := bytes.Index(data, needle)
		if i < 0 {
			continue
		}
		pos := i + len(needle)
		for pos%4 != 0 {
			pos++
		}
		var chars []uint16
		for ; pos+1 < len(data) && len(chars) < 256; pos += 2 {
			c := binary.LittleEndian.Uint16(data[pos:])
			if c == 0 {
				break
			}
			chars = append(chars, c)
		}
		value := strings.TrimSpace(string(utf16.Decode(chars)))
		switch key {
		case "ProductName":
			m.Title = value
		case "CompanyName":
			m.Author = value
		default:
			m.Set(strings.NewReplacer("FileDescription", "Description", "ProductVersion", "Version", "FileVersion", "File version", "LegalCopyright", "Copyright").Replace(key), value)
		}
	}
	return nil
}

func legacyWindows(f *os.File, m *Meta) error {
	f.Seek(0, 0)
	var h [64]byte
	if _, err := io.ReadFull(f, h[:]); err != nil {
		return err
	}
	s, err := f.Stat()
	if err != nil {
		return err
	}
	if string(h[:2]) != "MZ" {
		return errors.New("not DOS")
	}
	off := int64(binary.LittleEndian.Uint32(h[60:]))
	var peHeader [26]byte
	if off >= 64 && off+26 <= s.Size() {
		if _, err := f.ReadAt(peHeader[:], off); err == nil && string(peHeader[:4]) == "PE\x00\x00" {
			sections := int64(binary.LittleEndian.Uint16(peHeader[6:]))
			optional := int64(binary.LittleEndian.Uint16(peHeader[20:]))
			if binary.LittleEndian.Uint16(peHeader[4:]) != 0x200 || binary.LittleEndian.Uint16(peHeader[24:]) != 0x20b || optional < 112 || sections < 1 || off+24+optional+40*sections > s.Size() {
				return errors.New("unsupported or truncated PE")
			}
			m.Set("Architecture", "IA64")
			m.Set("Format", "PE32+")
			m.Set("Type", "Windows executable")
			if binary.LittleEndian.Uint16(peHeader[22:])&pe.IMAGE_FILE_DLL != 0 {
				m.Set("Type", "Windows library")
			}
			return nil
		}
	}
	pages := int64(binary.LittleEndian.Uint16(h[4:]))
	last := int64(binary.LittleEndian.Uint16(h[2:]))
	headerSize := int64(binary.LittleEndian.Uint16(h[8:])) * 16
	length := pages * 512
	if last > 0 {
		length = (pages-1)*512 + last
	}
	if pages == 0 || last > 511 || headerSize < 28 || headerSize > length || length > s.Size() {
		return errors.New("invalid DOS size")
	}
	m.Set("Format", "DOS MZ")
	m.Set("Architecture", "x86 16-bit")
	m.Set("Type", "DOS executable")
	return nil
}

func utf16le(s string) []byte {
	out := make([]byte, 0, len(s)*2)
	for _, c := range utf16.Encode([]rune(s)) {
		out = binary.LittleEndian.AppendUint16(out, c)
	}
	return out
}

var apkIcon = regexp.MustCompile(`^res/(?:mipmap|drawable)[^/]*/(?:ic_launcher|icon|app_icon|ic_launcher_round)\.(?:png|webp)$`)

func apk(z *zip.Reader, m *Meta) error {
	var icon *zip.File
	for _, f := range z.File {
		if apkIcon.MatchString(f.Name) && (icon == nil || f.UncompressedSize64 > icon.UncompressedSize64) {
			icon = f
		}
	}
	if icon != nil {
		m.Poster = "zip:" + icon.Name
	}
	data, err := readZip(z, "AndroidManifest.xml", 8<<20)
	if err != nil {
		return err
	}
	attrs, err := manifestAttributes(data)
	if err != nil {
		return err
	}
	m.Set("Package", attrs["manifest.package"])
	m.Set("Version", attrs["manifest.versionName"])
	m.Set("Version code", attrs["manifest.versionCode"])
	m.Set("Min SDK", attrs["uses-sdk.minSdkVersion"])
	m.Set("Target SDK", attrs["uses-sdk.targetSdkVersion"])
	if label := attrs["application.label"]; label != "" && !strings.HasPrefix(label, "@") {
		m.Title = label
	}
	return nil
}

// manifestAttributes decodes Android binary XML far enough to read the
// attributes of the manifest, uses-sdk and application elements.
func manifestAttributes(data []byte) (map[string]string, error) {
	if len(data) < 8 || binary.LittleEndian.Uint16(data) != 0x0003 {
		return nil, errors.New("invalid binary manifest")
	}
	var pool []string
	attrs := map[string]string{}
	str := func(i uint32) string {
		if int(i) < len(pool) {
			return pool[i]
		}
		return ""
	}
	for pos := 8; pos+8 <= len(data); {
		kind := binary.LittleEndian.Uint16(data[pos:])
		size := int(binary.LittleEndian.Uint32(data[pos+4:]))
		if size < 8 || pos+size > len(data) {
			break
		}
		chunk := data[pos : pos+size]
		switch kind {
		case 0x0001:
			pool = stringPool(chunk)
		case 0x0102:
			if len(chunk) < 36 {
				break
			}
			element := str(binary.LittleEndian.Uint32(chunk[20:]))
			if element != "manifest" && element != "uses-sdk" && element != "application" {
				break
			}
			start, count := int(binary.LittleEndian.Uint16(chunk[24:])), int(binary.LittleEndian.Uint16(chunk[28:]))
			for i := 0; i < count; i++ {
				a := 16 + start + i*20
				if a+20 > len(chunk) {
					break
				}
				name, raw := str(binary.LittleEndian.Uint32(chunk[a+4:])), binary.LittleEndian.Uint32(chunk[a+8:])
				dataType, value := chunk[a+15], binary.LittleEndian.Uint32(chunk[a+16:])
				switch {
				case raw != 0xFFFFFFFF:
					attrs[element+"."+name] = str(raw)
				case dataType == 0x10 || dataType == 0x11:
					attrs[element+"."+name] = strconv.FormatUint(uint64(value), 10)
				case dataType == 0x01:
					attrs[element+"."+name] = fmt.Sprintf("@0x%08x", value)
				}
			}
		}
		pos += size
	}
	return attrs, nil
}

func stringPool(chunk []byte) []string {
	if len(chunk) < 28 {
		return nil
	}
	count := int(binary.LittleEndian.Uint32(chunk[8:]))
	utf8 := binary.LittleEndian.Uint32(chunk[16:])&(1<<8) != 0
	start := int(binary.LittleEndian.Uint32(chunk[20:]))
	header := int(binary.LittleEndian.Uint16(chunk[2:]))
	pool := make([]string, 0, count)
	for i := 0; i < count && header+i*4+4 <= len(chunk); i++ {
		at := start + int(binary.LittleEndian.Uint32(chunk[header+i*4:]))
		if at >= len(chunk) {
			pool = append(pool, "")
			continue
		}
		if utf8 {
			lengthPrefix8(chunk, &at) // skip the UTF-16 length
			n := lengthPrefix8(chunk, &at)
			pool = append(pool, string(chunk[at:min(at+n, len(chunk))]))
			continue
		}
		n := int(binary.LittleEndian.Uint16(chunk[at:]))
		at += 2
		if n&0x8000 != 0 && at+2 <= len(chunk) {
			n = (n&0x7fff)<<16 | int(binary.LittleEndian.Uint16(chunk[at:]))
			at += 2
		}
		chars := make([]uint16, 0, n)
		for j := 0; j < n && at+2*j+2 <= len(chunk); j++ {
			chars = append(chars, binary.LittleEndian.Uint16(chunk[at+2*j:]))
		}
		pool = append(pool, string(utf16.Decode(chars)))
	}
	return pool
}

func lengthPrefix8(b []byte, at *int) int {
	if *at >= len(b) {
		return 0
	}
	n := int(b[*at])
	*at++
	if n&0x80 != 0 && *at < len(b) {
		n = (n&0x7f)<<8 | int(b[*at])
		*at++
	}
	return n
}
