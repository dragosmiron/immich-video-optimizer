package engine

import (
	"fmt"
	"sync"
	"testing"
)

func TestRingBuffer_WriteUnderCapacity(t *testing.T) {
	rb := NewRingBuffer(16)
	n, err := rb.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("expected write 5 bytes, got %d, err %v", n, err)
	}
	if rb.String() != "hello" {
		t.Fatalf("expected 'hello', got '%s'", rb.String())
	}
}

func TestRingBuffer_WriteWrapAround(t *testing.T) {
	rb := NewRingBuffer(8)
	_, _ = rb.Write([]byte("hello "))
	_, _ = rb.Write([]byte("world!"))

	// "hello world!" (12 bytes) -> last 8 bytes is "o world!"
	expected := "o world!"
	if rb.String() != expected {
		t.Fatalf("expected '%s', got '%s'", expected, rb.String())
	}
}

func TestRingBuffer_WriteLargerThanCapacity(t *testing.T) {
	rb := NewRingBuffer(5)
	_, _ = rb.Write([]byte("1234567890"))

	expected := "67890"
	if rb.String() != expected {
		t.Fatalf("expected '%s', got '%s'", expected, rb.String())
	}
}

func TestRingBuffer_ConcurrentWrites(t *testing.T) {
	rb := NewRingBuffer(128)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_, _ = fmt.Fprintf(rb, "msg-%02d ", id)
		}(i)
	}
	wg.Wait()

	if len(rb.Bytes()) == 0 {
		t.Fatalf("expected non-empty ring buffer after concurrent writes")
	}
}

func TestRingBuffer_Reset(t *testing.T) {
	rb := NewRingBuffer(8)
	_, _ = rb.Write([]byte("12345678"))
	rb.Reset()
	if rb.String() != "" {
		t.Fatalf("expected empty string after reset, got '%s'", rb.String())
	}
}
