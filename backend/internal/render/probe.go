package render

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"

	"lyrics/internal/domain"
)

type probeOutput struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func (engine *Engine) Probe(ctx context.Context, path string) (domain.MediaInfo, error) {
	command := exec.CommandContext(ctx, engine.probe,
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	)

	raw, err := command.Output()
	if err != nil {
		return domain.MediaInfo{}, fmt.Errorf("ffprobe no pudo leer el archivo: %w", err)
	}

	var decoded probeOutput
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return domain.MediaInfo{}, fmt.Errorf("no se pudo interpretar la salida de ffprobe: %w", err)
	}

	info := domain.MediaInfo{}
	for _, stream := range decoded.Streams {
		switch stream.CodecType {
		case "video":
			if stream.Width == 0 && stream.Height == 0 {
				continue
			}
			if !info.HasVideo {
				info.HasVideo = true
				info.Width = stream.Width
				info.Height = stream.Height
			}
		case "audio":
			info.HasAudio = true
		}
	}

	if !info.HasVideo && !info.HasAudio {
		return domain.MediaInfo{}, fmt.Errorf("el archivo no contiene ni video ni audio")
	}
	if value, err := strconv.ParseFloat(decoded.Format.Duration, 64); err == nil {
		info.Duration = value
	}
	return info, nil
}
