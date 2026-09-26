package engine

import (
	"reflect"
	"testing"
)

func TestBuildExiftoolArgs(t *testing.T) {
	original := "original.mp4"
	transcoded := "transcoded.mp4"

	expected := []string{
		"-TagsFromFile", "original.mp4",
		"-all:all",
		"-unsafe",
		"--Track*",
		"--Rotation",
		"-overwrite_original",
		"transcoded.mp4",
	}

	args := BuildExiftoolArgs(original, transcoded)
	if !reflect.DeepEqual(args, expected) {
		t.Errorf("expected %v, got %v", expected, args)
	}
}
