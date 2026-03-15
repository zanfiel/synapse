package sse

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseDataJoinsMultilineData(t *testing.T) {
	input := strings.NewReader("data: first\ndata: second\n\n")

	var events []string
	err := ParseData(input, func(data string) error {
		events = append(events, data)
		return nil
	})
	if err != nil {
		t.Fatalf("ParseData error: %v", err)
	}

	want := []string{"first\nsecond"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestParseDataFlushesFinalEventWithoutBlankLine(t *testing.T) {
	input := strings.NewReader("data: tail")

	var got string
	err := ParseData(input, func(data string) error {
		got = data
		return nil
	})
	if err != nil {
		t.Fatalf("ParseData error: %v", err)
	}
	if got != "tail" {
		t.Fatalf("got %q, want %q", got, "tail")
	}
}
