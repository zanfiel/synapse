package sse

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

var ErrStop = errors.New("stop sse")

// ParseData parses SSE frames and calls handler once per complete event payload.
// Consecutive data: lines are joined with newlines per the SSE spec.
func ParseData(body io.Reader, handler func(data string) error) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var dataLines []string
	flush := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = nil
		return handler(data)
	}

	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				if errors.Is(err, ErrStop) {
					return nil
				}
				return err
			}
			continue
		}

		if strings.HasPrefix(line, ":") {
			continue
		}

		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			if strings.HasPrefix(data, " ") {
				data = data[1:]
			}
			dataLines = append(dataLines, data)
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	if err := flush(); err != nil {
		if errors.Is(err, ErrStop) {
			return nil
		}
		return err
	}

	return nil
}
