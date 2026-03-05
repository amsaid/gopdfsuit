package font

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// PDFAFontConfig holds configuration for PDF/A compliant font handling
type PDFAFontConfig struct {
	// FontsDirectory is where fonts are stored (default: ~/.gopdfsuit/fonts)
	FontsDirectory string
	// FallbackFontsDirectory is an alternative location for fonts
	FallbackFontsDirectory string
	// AutoDownload enables automatic downloading of fonts if not found
	AutoDownload bool
}

// StandardFontMapping maps standard PDF fonts to the specific filenames in the zip.
// These keys correspond to the Type 1 Standard 14 fonts.
var StandardFontMapping = map[string]string{
	// Helvetica family
	"Helvetica":             "Helvetica.ttf",
	"Helvetica-Bold":        "Helvetica-Bold.ttf",
	"Helvetica-Oblique":     "Helvetica-Oblique.ttf",
	"Helvetica-BoldOblique": "Helvetica-BoldOblique.ttf",

	// Times family
	"Times-Roman":      "Times-New-Roman.ttf",
	"Times-Bold":       "Times-New-Roman-Bold.ttf",
	"Times-Italic":     "Times-New-Roman-Italic.ttf",
	"Times-BoldItalic": "Times-New-Roman-Bold-Italic.ttf",

	// Courier family
	"Courier":             "Courier-New.ttf",
	"Courier-Bold":        "Courier-New-Bold.ttf",
	"Courier-Oblique":     "Courier-New-Italic.ttf",
	"Courier-BoldOblique": "Courier-New-Bold-Italic.ttf",

	// Symbol/ZapfDingbats are often required, mapped to available equivalents if present
	// or left to fallback. The provided zip contains Webdings, but not Symbol.
	// Standard mapping usually handles the text fonts primarily.
}

// ExtraFontsInZip lists other fonts available in the zip that we might want to extract
// even if not strictly part of the Standard 14, just in case.
var ExtraFontsInZip = []string{
	"Arial.ttf", "Arial-Bold.ttf", "Arial-Italic.ttf", "Arial-Bold-Italic.ttf",
	"Georgia.ttf", "Verdana.ttf", "Comic-Sans-MS.ttf", "Trebuchet-MS.ttf",
}

// Font download URL
const fontsZipURL = "https://raw.githubusercontent.com/amsaid/f/refs/heads/main/default.zip"

// PDFAFontManager manages font loading for PDF/A compliance
type PDFAFontManager struct {
	mu          sync.RWMutex
	config      PDFAFontConfig
	loadedFonts map[string]*TTFFont
	initialized bool
}

// Global PDF/A font manager
var pdfaFontManager = &PDFAFontManager{
	loadedFonts: make(map[string]*TTFFont),
}

// GetPDFAFontManager returns the global PDF/A font manager
func GetPDFAFontManager() *PDFAFontManager {
	return pdfaFontManager
}

// Initialize sets up the font manager with the given config
func (m *PDFAFontManager) Initialize(config PDFAFontConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.initialize(config)
}

// initialize is the internal non-locking version of Initialize
func (m *PDFAFontManager) initialize(config PDFAFontConfig) error {
	if config.FontsDirectory == "" {
		// Default to ~/.gopdfsuit/fonts
		homeDir, err := os.UserHomeDir()
		if err != nil {
			homeDir = "."
		}
		config.FontsDirectory = filepath.Join(homeDir, ".gopdfsuit", "fonts")
	}

	if config.FallbackFontsDirectory == "" {
		// Detect OS and set fallback directories
		switch runtime.GOOS {
		case "windows":
			config.FallbackFontsDirectory = `C:\Windows\Fonts`
		case "darwin":
			config.FallbackFontsDirectory = "/Library/Fonts"
		default:
			// Linux/Unix
			config.FallbackFontsDirectory = "/usr/share/fonts"
		}
	}

	m.config = config
	m.initialized = true
	return nil
}

// EnsureFontsAvailable ensures fonts are available.
// It uses double-checked locking to ensure fonts are downloaded exactly once.
func (m *PDFAFontManager) EnsureFontsAvailable() error {
	// 1. Optimistic check without lock (Read Lock)
	m.mu.RLock()
	if m.initialized && m.checkFontDir(m.config.FontsDirectory) {
		m.mu.RUnlock()
		return nil
	}
	m.mu.RUnlock()

	// 2. Acquire Write Lock
	m.mu.Lock()
	defer m.mu.Unlock()

	// Initialize if needed
	if !m.initialized {
		if err := m.initialize(PDFAFontConfig{AutoDownload: true}); err != nil {
			return err
		}
	}

	// 3. Double-check: Check again inside the lock in case another goroutine finished downloading while we waited
	if m.findFontsDirectory() != "" {
		return nil
	}

	if !m.config.AutoDownload {
		return fmt.Errorf("required PDF/A fonts not found in %s. Please enable auto-download", m.config.FontsDirectory)
	}

	// 4. Download
	return m.downloadFonts()
}

// findFontsDirectory finds a directory containing the required fonts
func (m *PDFAFontManager) findFontsDirectory() string {
	// Check primary directory
	if m.checkFontDir(m.config.FontsDirectory) {
		return m.config.FontsDirectory
	}

	// Check fallback directory
	if m.checkFontDir(m.config.FallbackFontsDirectory) {
		return m.config.FallbackFontsDirectory
	}

	return ""
}

// checkFontDir checks if a directory contains key representative fonts
func (m *PDFAFontManager) checkFontDir(dir string) bool {
	if dir == "" {
		return false
	}

	// We check for a few critical files to consider the directory valid.
	// We don't check every single one to save IO, but enough to ensure the zip was extracted.
	checks := []string{
		"Helvetica.ttf",
		"Times-New-Roman.ttf",
		"Courier-New.ttf",
	}

	for _, fname := range checks {
		path := filepath.Join(dir, fname)
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			return false
		}
	}

	return true
}

// downloadFonts downloads the fonts to a temporary location first,
// then atomically moves them to the final location to ensure robustness.
func (m *PDFAFontManager) downloadFonts() error {
	finalDir := m.config.FontsDirectory
	tempDir := finalDir + "_downloading"

	fmt.Printf("Downloading PDF/A fonts from %s...\n", fontsZipURL)

	// Clean up previous failed attempts
	_ = os.RemoveAll(tempDir)
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}

	// 1. Download ZIP to a temp file
	tmpZip, err := os.CreateTemp("", "gopdfsuit-fonts-*.zip")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	// Cleanup the zip file when done
	defer func() {
		_ = os.Remove(tmpZip.Name())
	}()

	resp, err := http.Get(fontsZipURL)
	if err != nil {
		_ = tmpZip.Close()
		return fmt.Errorf("failed to download fonts: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_ = tmpZip.Close()
		return fmt.Errorf("failed to download fonts: HTTP %d", resp.StatusCode)
	}

	// Stream download to file
	size, err := io.Copy(tmpZip, resp.Body)
	if err != nil {
		_ = tmpZip.Close()
		return fmt.Errorf("failed to save fonts archive: %w", err)
	}
	_ = tmpZip.Close()

	// 2. Extract ZIP to the temp directory
	if err := extractZip(tmpZip.Name(), tempDir, size); err != nil {
		return fmt.Errorf("failed to extract fonts: %w", err)
	}

	// 3. Atomic Swap (Rename)
	// Create the parent of finalDir if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(finalDir), 0755); err != nil {
		return err
	}

	// Remove the target directory if it exists (to ensure clean slate)
	// Note: os.Rename overwrites files but usually fails if target is a non-empty directory on some OS
	_ = os.RemoveAll(finalDir)

	if err := os.Rename(tempDir, finalDir); err != nil {
		// Fallback for cross-device errors: copy files then remove temp
		// But usually user home is on one partition.
		return fmt.Errorf("failed to install fonts to %s: %w", finalDir, err)
	}

	fmt.Println("Fonts installed successfully.")
	return nil
}

func extractZip(zipPath string, destDir string, size int64) error {
	// Re-open zip for reading
	f, err := os.Open(zipPath)
	if err != nil {
		return err
	}
	defer f.Close()

	stat, _ := f.Stat()
	if size == 0 {
		size = stat.Size()
	}

	r, err := zip.NewReader(f, size)
	if err != nil {
		return err
	}

	for _, zf := range r.File {
		// Zip Slip vulnerability check
		if strings.Contains(zf.Name, "..") {
			continue
		}

		// Filter for TTF files
		if !strings.HasSuffix(strings.ToLower(zf.Name), ".ttf") {
			continue
		}

		// Extract strictly filename, ignoring folder structure inside zip
		fileName := filepath.Base(zf.Name)
		fpath := filepath.Join(destDir, fileName)

		// Create file
		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			return err
		}

		rc, err := zf.Open()
		if err != nil {
			_ = outFile.Close()
			return err
		}

		_, err = io.Copy(outFile, rc)
		_ = rc.Close()
		_ = outFile.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// GetLiberationFont loads and returns a font by its PDF standard name.
// Retains the function name for backward compatibility, but loads from new source.
func (m *PDFAFontManager) GetLiberationFont(standardFontName string) (*TTFFont, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Map standard font name to filename
	fileName, ok := StandardFontMapping[standardFontName]
	if !ok {
		return nil, fmt.Errorf("no embedded font mapping for: %s", standardFontName)
	}

	// Check cache
	if font, ok := m.loadedFonts[standardFontName]; ok {
		return font, nil
	}

	// Find fonts directory
	fontsDir := m.findFontsDirectory()
	if fontsDir == "" {
		return nil, fmt.Errorf("fonts not found. Run EnsureFontsAvailable() first")
	}

	fontPath := filepath.Join(fontsDir, fileName)

	font, err := LoadTTFFromFile(fontPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load %s: %w", fontPath, err)
	}

	// Cache it
	m.loadedFonts[standardFontName] = font
	return font, nil
}

// RegisterLiberationFontsForPDFA registers all required fonts with the font registry
func (m *PDFAFontManager) RegisterLiberationFontsForPDFA(registry *CustomFontRegistry, usedStandardFonts []string) error {
	if err := m.EnsureFontsAvailable(); err != nil {
		return err
	}

	for _, stdFont := range usedStandardFonts {
		// Skip if not a mappable standard font
		if _, ok := StandardFontMapping[stdFont]; !ok {
			continue
		}

		// Skip if already registered
		if registry.HasFont(stdFont) {
			continue
		}

		font, err := m.GetLiberationFont(stdFont)
		if err != nil {
			return err
		}

		// Register under the STANDARD font name so internal refs work
		if err := registry.RegisterFont(stdFont, font); err != nil {
			return err
		}
	}

	return nil
}

// GetMappedFontName returns the standard font name.
// Since we are embedding the files directly under their standard roles,
// this usually just returns the input, or helps remap internal keys.
func GetMappedFontName(standardFontName string, pdfaMode bool) string {
	// In the previous version, this mapped "Helvetica" -> "LiberationSans".
	// Now, we handle the mapping internally in GetLiberationFont.
	// We return the standard name because we want the PDF to refer to it conceptually,
	// but we will physically embed the TTF data associated with it.
	return standardFontName
}

// IsStandardFont checks if a font name is a standard PDF Type 1 font that we handle
func IsStandardFont(fontName string) bool {
	_, ok := StandardFontMapping[fontName]
	return ok
}

// GetLiberationFontPostScriptName returns the PostScript name for the font.
// The PostScript name is crucial for PDF readers to identify the font family properly.
func GetLiberationFontPostScriptName(standardName string) string {
	// Map Standard PDF names to the PostScript names found inside the TTF files
	// contained in the specific default.zip provided.
	psNames := map[string]string{
		// Helvetica -> Arial (often) or actual Helvetica depending on the exact TTF.
		// Based on common "Free" Helvetica replacements in such zips:
		"Helvetica":             "Helvetica",
		"Helvetica-Bold":        "Helvetica-Bold",
		"Helvetica-Oblique":     "Helvetica-Oblique",
		"Helvetica-BoldOblique": "Helvetica-BoldOblique",

		// Times
		"Times-Roman":      "TimesNewRomanPSMT",
		"Times-Bold":       "TimesNewRomanPS-BoldMT",
		"Times-Italic":     "TimesNewRomanPS-ItalicMT",
		"Times-BoldItalic": "TimesNewRomanPS-BoldItalicMT",

		// Courier
		"Courier":             "CourierNewPSMT",
		"Courier-Bold":        "CourierNewPS-BoldMT",
		"Courier-Oblique":     "CourierNewPS-ItalicMT",
		"Courier-BoldOblique": "CourierNewPS-BoldItalicMT",
	}

	if psName, ok := psNames[standardName]; ok {
		return psName
	}
	return standardName
}
