package diwrapper

import (
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"sync"
	"time"

	"github.com/facebookgo/inject"
)

type Initializer interface {
	Init() error
}

// Cleaners are called when everything ends, note that Stop() must be called explicitly.
type Cleaner interface {
	Clean() error
}

type InjectWrapper struct {
	g *inject.Graph
	// this slice is here because we want to initialize objects in the order as they are added (after the graph is generated):
	objects []*inject.Object
	stopped bool
}

// NewDebug starts a diwrapper with debug output
func NewDebug() *InjectWrapper {
	di := New()
	di.g.Logger = &log{}
	return di
}

func New() *InjectWrapper {
	var g inject.Graph
	return &InjectWrapper{
		g:       &g,
		objects: []*inject.Object{},
	}
}

func (i *InjectWrapper) log(format string, v ...interface{}) {
	if i.g.Logger != nil {
		i.g.Logger.Debugf(format, v...)
	}
}

func (i *InjectWrapper) WithObjects(objects ...interface{}) *InjectWrapper {
	for _, obj := range objects {
		i.WithObject(obj)
	}
	return i
}

func (i *InjectWrapper) WithObject(object interface{}) *InjectWrapper {
	i.log("Adding %T", object)
	o := &inject.Object{Value: object}
	if err := i.g.Provide(o); err != nil {
		panic(fmt.Sprintf("Error providing object %T:%s", object, err.Error()))
	}
	i.objects = append(i.objects, o)
	return i
}

// WithObjectOrErr is a helper methods to be used with initializers which return a pointer and error
func (i *InjectWrapper) WithObjectOrErr(object interface{}, err error) *InjectWrapper {
	if err != nil {
		panic(err)
	}
	i.log("Adding %T", object)
	o := &inject.Object{Value: object}
	if err := i.g.Provide(o); err != nil {
		panic(fmt.Sprintf("Error providing object %T:%s", object, err.Error()))
	}
	i.objects = append(i.objects, o)
	return i
}

func (i *InjectWrapper) WithNamedObject(name string, obj interface{}) *InjectWrapper {
	i.log("Adding %s: %T", name, obj)
	o := &inject.Object{Name: name, Value: obj}
	if err := i.g.Provide(o); err != nil {
		panic(fmt.Sprintf("Error providing named object %s.%T:%s", name, obj, err.Error()))
	}
	i.objects = append(i.objects, o)
	return i
}

func (i *InjectWrapper) AllObjects() []interface{} {
	//if len(i.g.Objects()) != len(i.objects) { panic(fmt.Sprintf("Invalid objects size: %d!=%d", len(i.g.Objects()), len(i.objects))) }
	res := []interface{}{}
	for _, diObj := range i.objects {
		res = append(res, diObj.Value)
	}
	return res
}

// MustFindObject privides an object of the specified type and name (name can be empty for unnamed objects). Note that
// this function is only for debugging and testing. In production, objects should be used injected and never retrieved
// with this. That's why this method panics!
func (i InjectWrapper) MustGetNamedObject(sample interface{}, name string) interface{} {
	sampleType := reflect.TypeOf(sample)
	if sampleType.Kind() != reflect.Ptr {
		panic(fmt.Sprintf("Sample must be interface, found %T", sample))
	}
	for _, obj := range i.objects {
		if reflect.TypeOf(obj.Value) == sampleType && obj.Name == name {
			return obj.Value
		}
	}
	panic(fmt.Sprintf("Object not found: %s.%T", name, sample))
}

// MustGetObject: see MustGetNamedObject
func (i InjectWrapper) MustGetObject(sample interface{}) interface{} {
	return i.MustGetNamedObject(sample, "")
}

func (i *InjectWrapper) CheckNoImplicitObjects() *InjectWrapper {
	for _, o := range i.g.Objects() {
		var oOK bool
		for _, diwrapperObj := range i.objects {
			if diwrapperObj.Value == o.Value {
				oOK = true
			}
		}
		if oOK {
			i.log("%T OK\n", o.Value)
		} else {
			panic(fmt.Sprintf("%T not explicitly created", o.Value))
		}
	}

	return i
}

// InitializeGraphWithImplicitObjects initializes a graph allowing implicitly created objects. Those are objects not specified with one of the With...() methods.
func (i *InjectWrapper) InitializeGraphWithImplicitObjects() *InjectWrapper {
	i.log("Initializing %d objects", len(i.objects))

	if err := i.g.Populate(); err != nil {
		panic(fmt.Sprintf("Error populating graph: %s", err))
	}
	for _, obj := range i.AllObjects() {
		if initializer, is := obj.(Initializer); is {
			i.log("Initializing %T", obj)
			if err := initializer.Init(); err != nil {
				panic(fmt.Sprintf("Error initializing privided object %T:%s", obj, err.Error()))
			}
			i.log("Initialized %T", obj)
		}
	}

	return i
}

// InitializeGraph initializes a graph, but fails if an object is not specified with one of the With() methods.
func (i *InjectWrapper) InitializeGraph() *InjectWrapper {
	_ = i.InitializeGraphWithImplicitObjects()
	return i.CheckNoImplicitObjects()
}

func (i *InjectWrapper) WithCleanBeforeShutdown(maxDuration time.Duration, sig ...os.Signal) *InjectWrapper {
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, sig...)
		s := <-c
		fmt.Printf("Got signal %v, cleaning before exit...\n", s.String())
		i.Stop(maxDuration)
	}()
	return i
}

func (i *InjectWrapper) Stop(maxDuration time.Duration) {
	if i.stopped {
		fmt.Fprintf(os.Stderr, "Stop already called")
		return
	}
	i.stopped = true

	errorsChan := make(chan error, len(i.AllObjects()))
	wg := new(sync.WaitGroup)
	for _, obj := range i.AllObjects() {
		if cleaner, is := obj.(Cleaner); is {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := i.cleanCleanable(cleaner, maxDuration); err != nil {
					errorsChan <- err
				}
			}()
		}
	}
	wg.Wait()

	if len(errorsChan) == 0 {
		i.log("all cleaned => exit")
		os.Exit(0)
	}
	i.log("NOT all cleaned => exit")
	os.Exit(1)
}

func (i *InjectWrapper) cleanCleanable(cleaner Cleaner, maxDuratiDuration time.Duration) error {
	done := make(chan bool)
	errorChan := make(chan error)
	timeoutChan := time.After(maxDuratiDuration)

	go func() {
		i.log("Cleaning %T", cleaner)
		defer i.log("Cleaned %T", cleaner)
		if err := cleaner.Clean(); err != nil {
			msg := fmt.Sprintf("Error cleaning %T: %+v", cleaner, err)
			i.log(msg)
			fmt.Fprintln(os.Stderr, msg)
			errorChan <- err
		}
		done <- true
	}()

	for true {
		select {
		case err := <-errorChan:
			return err
		case <-done:
			return nil
		case <-timeoutChan:
			msg := fmt.Sprintf("Cleaning %T took more than %s", cleaner, maxDuratiDuration.String())
			fmt.Fprintln(os.Stderr, msg)
			i.log(msg)
			return fmt.Errorf(msg)
		case <-time.After(time.Second):
			i.log("Still cleaning %T\n", cleaner)
		}
	}

	return nil
}

func (i *InjectWrapper) Stopper(maxDuration time.Duration) func() {
	return func() {
		i.Stop(maxDuration)
	}
}

type log struct{}

func (l *log) Debugf(format string, v ...interface{}) {
	fmt.Printf(format+"\n", v...)
}
