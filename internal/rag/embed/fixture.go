package embed

import (
	"context"
	"encoding/binary"
	"errors"
	"hash/fnv"
	"sync/atomic"
)

const (
	FixtureModel      = "fixture"
	defaultFixtureDim = 4
)

// Fixture is a deterministic embedder for tests. ErrAt is a one-based call
// index. FailOn, when set, is evaluated on every call before vectors are built.
type Fixture struct {
	Dimension int
	ErrAt     int
	Err       error
	FailOn    func(call int, texts []string) error

	calls atomic.Int64
}

// NewFixture constructs a deterministic fixture. Its default dimension is four.
func NewFixture(dimension ...int) *Fixture {
	fixture := &Fixture{Dimension: defaultFixtureDim}
	if len(dimension) > 0 {
		fixture.Dimension = dimension[0]
	}
	return fixture
}

func (*Fixture) Model() string { return FixtureModel }

func (fixture *Fixture) Dim() int {
	if fixture.Dimension <= 0 {
		return defaultFixtureDim
	}
	return fixture.Dimension
}

// Calls returns the number of Embed invocations.
func (fixture *Fixture) Calls() int {
	return int(fixture.calls.Load())
}

func (fixture *Fixture) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	call := int(fixture.calls.Add(1))
	if fixture.FailOn != nil {
		if err := fixture.FailOn(call, texts); err != nil {
			return nil, err
		}
	}
	if fixture.ErrAt > 0 && call == fixture.ErrAt {
		if fixture.Err != nil {
			return nil, fixture.Err
		}
		return nil, errors.New("fixture embed failure")
	}

	vectors := make([][]float32, len(texts))
	for index, text := range texts {
		vectors[index] = fixtureVector(text, fixture.Dim())
	}
	return vectors, nil
}

func fixtureVector(text string, dimension int) []float32 {
	vector := make([]float32, dimension)
	var coordinate [4]byte
	for index := range vector {
		hash := fnv.New32a()
		_, _ = hash.Write([]byte(text))
		binary.LittleEndian.PutUint32(coordinate[:], uint32(index))
		_, _ = hash.Write(coordinate[:])
		unit := float64(hash.Sum32()) / float64(^uint32(0))
		vector[index] = float32(unit*2 - 1)
	}
	return vector
}
