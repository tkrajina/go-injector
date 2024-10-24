package injector

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type InitializableStruct struct {
	initialized bool
}

func (s *InitializableStruct) Init() error {
	s.initialized = true
	return nil
}

var _ Initializer = (*InitializableStruct)(nil)

type StoppableStruct struct {
	Stopped bool
}

func (s *StoppableStruct) Clean() error {
	s.Stopped = true
	return nil
}

var _ Cleaner = (*StoppableStruct)(nil)

func TestSimple(t *testing.T) {
	s := InitializableStruct{}

	New().
		WithObject(&s).
		InitializeGraph()

	assert.True(t, s.initialized)
}

func TestInitialization(t *testing.T) {
	type Aaa1 struct{}
	type Bbb1 struct {
		Aaa1 *Aaa1 `inject:""`
	}

	b := new(Bbb1)

	New().
		WithObjects(new(Aaa1), b).
		InitializeGraph()

	assert.NotNil(t, b.Aaa1, "b=%#v", b)
}

func TestInjectionIgnoreNotProvided(t *testing.T) {
	type Aaa1 struct{}
	type Bbb1 struct {
		Aaa1 *Aaa1 `inject:""`
	}

	defer func() {
		r := recover()
		fmt.Printf("r=%#v\n", r)
		assert.NotNil(t, r)
		assert.Equal(t, "No candidates for *injector.Bbb1.Aaa1 (*injector.Aaa1)", r)
	}()

	b := new(Bbb1)
	_ = NewDebug().
		WithObjects(b).
		InitializeGraph()
	t.Fail()
}

func TestInjectionNotProvided(t *testing.T) {

	defer func() {
		if r := recover(); r != nil {
			assert.Equal(t, fmt.Sprintf("%v", r), "No candidates for *injector.Bbb1.Aaa1 (*injector.Aaa1)")
		}
	}()

	type Aaa1 struct{}
	type Bbb1 struct {
		Aaa1 *Aaa1 `inject:""`
	}

	b := new(Bbb1)
	_ = NewDebug().
		WithObjects(b).
		InitializeGraph()

	// Must not reach this point, because Aaa1 is not defined in the initialization, must fail
	t.FailNow()
}

func TestStopping(t *testing.T) {

	obj := StoppableStruct{}

	di := New().
		WithObjects(&obj).
		InitializeGraph()

	// This will usually be called in defer:
	di.Stop(time.Minute, false)

	assert.True(t, obj.Stopped)
}

func TestNamed(t *testing.T) {
	type Aaa struct{}
	type Bbb struct {
		Aaa *Aaa `inject:"aaa"`
	}

	b := new(Bbb)

	di := New().
		WithNamedObject("aaa", new(Aaa)).
		WithObject(b).
		InitializeGraph()

	assert.NotNil(t, b.Aaa)

	aaaObj := di.MustGetNamedObject(&Aaa{}, "aaa")
	assert.NotNil(t, aaaObj)

	bbbObj := di.MustGetObject(&Bbb{})
	assert.NotNil(t, bbbObj)
}

func TestInvalidNamed(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("Recovered in f", r)
		}
	}()

	type Aaa struct{}
	type Bbb struct {
		Aaa *Aaa `inject:"unknown_aaa"`
	}

	b := new(Bbb)

	New().
		WithNamedObject("aaa", new(Aaa)).
		WithObject(b).
		InitializeGraph()

	assert.Fail(t, "Must panic")
}

func TestDoubleType(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("Recovered in f", r)
		}
	}()

	type Aaa struct{}

	New().
		WithObject(new(Aaa)).
		WithObject(new(Aaa)).
		InitializeGraph()

	assert.Fail(t, "Must panic")
}

func TestDoubleNamed(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("Recovered in f", r)
		}
	}()

	type Aaa struct{}

	New().
		WithNamedObject("aaa", new(Aaa)).
		WithNamedObject("aaa", new(Aaa)).
		InitializeGraph()

	assert.Fail(t, "Must panic")
}

func TestInlineOverwritingFields(t *testing.T) {
	t.Parallel()

	type Logger struct{}
	type Aaa struct {
		Logger *Logger `inject:""`
	}
	type Bbb struct {
		Aaa    `inject:"inline"`
		Logger *Logger `inject:""`
	}
	type Service struct {
		Bbb `inject:"inline"`
	}
	logger := new(Logger)
	service := new(Service)
	New().
		WithObject(service).
		WithObject(logger).
		InitializeGraph()
	assert.Equal(t, logger, service.Logger)
	assert.Equal(t, logger, service.Bbb.Logger)
	assert.Equal(t, logger, service.Aaa.Logger)
	assert.Equal(t, logger, service.Bbb.Aaa.Logger)
}
