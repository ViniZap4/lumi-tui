package image

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

var imageRegex = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)

// Cache which renderer is available to avoid repeated exec.LookPath calls.
var (
	rendererOnce sync.Once
	rendererName string // "kitty", "iterm2", "timg", "chafa", "viu", ""
	insideTmux   bool
)

func detectRenderer() {
	rendererOnce.Do(func() {
		// Detect if inside tmux
		insideTmux = os.Getenv("TMUX") != ""

		// External CLI tools produce ANSI text (half-block/braille characters) which
		// works reliably inside Bubbletea's full-screen renderer and tmux.
		// Prefer these over native protocols which conflict with alt-screen redraws.
		for _, tool := range []string{"timg", "chafa", "viu"} {
			if _, err := exec.LookPath(tool); err == nil {
				rendererName = tool
				return
			}
		}

		// Fall back to native terminal image protocols.
		// These work but may flicker on screen redraws in full-screen TUIs.
		term := os.Getenv("TERM")
		termProgram := os.Getenv("TERM_PROGRAM")
		kittyWindowID := os.Getenv("KITTY_WINDOW_ID")
		lcTerminal := os.Getenv("LC_TERMINAL")       // Persists inside tmux (set by iTerm2)
		weztermExe := os.Getenv("WEZTERM_EXECUTABLE") // Persists inside tmux (set by WezTerm)

		// Direct detection (works outside tmux, sometimes inside)
		if kittyWindowID != "" || strings.Contains(term, "kitty") {
			rendererName = "kitty"
			return
		}
		if termProgram == "iTerm.app" || termProgram == "WezTerm" {
			rendererName = "iterm2"
			return
		}

		// Detection via env vars that persist inside tmux
		if lcTerminal == "iTerm2" {
			rendererName = "iterm2"
			return
		}
		if weztermExe != "" {
			rendererName = "iterm2" // WezTerm supports iTerm2 inline image protocol
			return
		}

		// If inside tmux, query the outer terminal
		if insideTmux {
			if name := detectFromTmux(); name != "" {
				rendererName = name
				return
			}
		}
	})
}

// detectFromTmux queries tmux for the outer terminal identity.
func detectFromTmux() string {
	// Check the client terminal name (e.g. "xterm-kitty")
	if out, err := exec.Command("tmux", "display-message", "-p", "#{client_termname}").Output(); err == nil {
		termname := strings.ToLower(strings.TrimSpace(string(out)))
		if strings.Contains(termname, "kitty") {
			return "kitty"
		}
	}

	// Check tmux server's captured environment for TERM_PROGRAM
	if out, err := exec.Command("tmux", "show-environment", "-g", "TERM_PROGRAM").Output(); err == nil {
		line := strings.TrimSpace(string(out))
		if val, ok := strings.CutPrefix(line, "TERM_PROGRAM="); ok {
			switch val {
			case "iTerm.app":
				return "iterm2"
			case "WezTerm":
				return "iterm2"
			}
		}
	}

	// Check tmux server's captured LC_TERMINAL
	if out, err := exec.Command("tmux", "show-environment", "-g", "LC_TERMINAL").Output(); err == nil {
		line := strings.TrimSpace(string(out))
		if val, ok := strings.CutPrefix(line, "LC_TERMINAL="); ok {
			if val == "iTerm2" {
				return "iterm2"
			}
		}
	}

	return ""
}

// wrapTmuxPassthrough wraps an escape sequence for tmux DCS passthrough.
// All ESC (\033) bytes inside the sequence are doubled so tmux forwards them
// to the outer terminal. Requires tmux 3.3a+ with `set -g allow-passthrough on`.
func wrapTmuxPassthrough(seq string) string {
	escaped := strings.ReplaceAll(seq, "\033", "\033\033")
	return "\033Ptmux;" + escaped + "\033\\"
}

var videoExtensions = map[string]bool{
	".mp4": true, ".webm": true, ".mov": true, ".avi": true,
	".mkv": true, ".ogg": true, ".ogv": true,
}

// IsVideo returns true if the path has a video file extension.
func IsVideo(path string) bool {
	return videoExtensions[strings.ToLower(filepath.Ext(path))]
}

// IsPdf returns true if the path has a .pdf extension.
func IsPdf(path string) bool {
	return strings.ToLower(filepath.Ext(path)) == ".pdf"
}

// IsEmbed returns true if the URL is an embeddable service (YouTube, Vimeo).
func IsEmbed(src string) bool {
	return strings.Contains(src, "youtube.com/") ||
		strings.Contains(src, "youtu.be/") ||
		strings.Contains(src, "vimeo.com/")
}

func HasImage(line string) bool {
	return imageRegex.MatchString(line)
}

func ExtractImagePath(line string) string {
	matches := imageRegex.FindStringSubmatch(line)
	if len(matches) > 2 {
		return matches[2]
	}
	return ""
}

func GetImagePath(line, notePath string) string {
	imgPath := ExtractImagePath(line)
	if imgPath == "" {
		return ""
	}
	// Skip URL-style paths (http/https) — only resolve local files
	if strings.HasPrefix(imgPath, "http://") || strings.HasPrefix(imgPath, "https://") {
		return ""
	}
	if !filepath.IsAbs(imgPath) {
		imgPath = filepath.Join(filepath.Dir(notePath), imgPath)
	}
	// Resolve to absolute to avoid CWD-dependent failures
	if abs, err := filepath.Abs(imgPath); err == nil {
		imgPath = abs
	}
	return imgPath
}

// Render renders an image for terminal display at the given width.
// Height is computed proportionally, capped at a reasonable max.
func Render(imagePath string, width int) string {
	if _, err := os.Stat(imagePath); err != nil {
		return fmt.Sprintf("[Image not found: %s]", filepath.Base(imagePath))
	}

	if width < 10 {
		width = 10
	}
	if width > 80 {
		width = 80
	}

	// Height: roughly 1/4 of width, accounting for terminal cell aspect ratio.
	// Terminal cells are ~2:1 (width:height), so width/4 gives a balanced image.
	height := width / 4
	if height < 6 {
		height = 6
	}
	if height > 20 {
		height = 20
	}

	detectRenderer()

	switch rendererName {
	case "kitty":
		return renderKittyProtocol(imagePath)
	case "iterm2":
		return renderITerm2Protocol(imagePath)
	case "timg":
		if out, err := exec.Command("timg", "-g", fmt.Sprintf("%dx%d", width, height), imagePath).Output(); err == nil {
			return strings.TrimSpace(string(out))
		}
	case "chafa":
		args := []string{"-s", fmt.Sprintf("%dx%d", width, height), "--animate=off"}
		if insideTmux {
			args = append(args, "--passthrough=tmux")
		}
		args = append(args, imagePath)
		if out, err := exec.Command("chafa", args...).Output(); err == nil {
			return strings.TrimSpace(string(out))
		}
	case "viu":
		if out, err := exec.Command("viu", "-w", fmt.Sprintf("%d", width), "-h", fmt.Sprintf("%d", height), imagePath).Output(); err == nil {
			return strings.TrimSpace(string(out))
		}
	}

	return fmt.Sprintf("[Image: %s] (install timg, chafa, or viu for preview)", filepath.Base(imagePath))
}

// RenderVideo extracts the first frame of a video using ffmpeg and renders it
// as an image. Returns a styled placeholder if ffmpeg is unavailable.
func RenderVideo(videoPath string, width int) string {
	if _, err := os.Stat(videoPath); err != nil {
		return fmt.Sprintf("[Video not found: %s]", filepath.Base(videoPath))
	}

	// Use ffmpeg to extract first frame as a temporary PNG
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		tmpFile, err := os.CreateTemp("", "lumi-thumb-*.png")
		if err == nil {
			tmpPath := tmpFile.Name()
			tmpFile.Close()
			defer os.Remove(tmpPath)

			cmd := exec.Command("ffmpeg", "-y", "-loglevel", "error",
				"-i", videoPath, "-vframes", "1", "-q:v", "2", tmpPath)
			if err := cmd.Run(); err == nil {
				if info, statErr := os.Stat(tmpPath); statErr == nil && info.Size() > 0 {
					rendered := Render(tmpPath, width)
					if !strings.HasPrefix(rendered, "[") {
						label := fmt.Sprintf("  ▶ %s", filepath.Base(videoPath))
						return rendered + "\n" + label
					}
				}
			}
		}
	}

	return fmt.Sprintf("[Video: %s] (install ffmpeg for thumbnail preview)", filepath.Base(videoPath))
}

// renderKittyProtocol uses the Kitty graphics protocol to display images inline.
// When inside tmux, each chunk is wrapped in DCS passthrough.
func renderKittyProtocol(imagePath string) string {
	data, err := os.ReadFile(imagePath)
	if err != nil {
		return fmt.Sprintf("[Image error: %s]", filepath.Base(imagePath))
	}

	encoded := base64.StdEncoding.EncodeToString(data)

	// Kitty graphics protocol: transmit image in chunks
	var sb strings.Builder
	const chunkSize = 4096

	for i := 0; i < len(encoded); i += chunkSize {
		end := i + chunkSize
		more := 1
		if end >= len(encoded) {
			end = len(encoded)
			more = 0
		}
		chunk := encoded[i:end]

		var seq string
		if i == 0 {
			// First chunk: include format and action
			seq = fmt.Sprintf("\033_Ga=T,f=100,m=%d;%s\033\\", more, chunk)
		} else {
			seq = fmt.Sprintf("\033_Gm=%d;%s\033\\", more, chunk)
		}

		if insideTmux {
			sb.WriteString(wrapTmuxPassthrough(seq))
		} else {
			sb.WriteString(seq)
		}
	}

	return sb.String()
}

// renderITerm2Protocol uses the iTerm2 inline image protocol.
// When inside tmux, the sequence is wrapped in DCS passthrough.
// Uses ST (\033\\) terminator instead of BEL (\a) for tmux compatibility.
func renderITerm2Protocol(imagePath string) string {
	data, err := os.ReadFile(imagePath)
	if err != nil {
		return fmt.Sprintf("[Image error: %s]", filepath.Base(imagePath))
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	name := base64.StdEncoding.EncodeToString([]byte(filepath.Base(imagePath)))

	// Use ST (\033\\) instead of BEL (\a) — BEL is not reliably forwarded through tmux
	seq := fmt.Sprintf("\033]1337;File=name=%s;inline=1;preserveAspectRatio=1:%s\033\\",
		name, encoded)

	if insideTmux {
		return wrapTmuxPassthrough(seq)
	}
	return seq
}
