package injector

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"sync"
	"time"

	"github.com/tkrajina/go-reflector/reflector"
)

type Object struct {
	Name  string
	Value any
}

type Initializer interface {
	Init() error
}

// Cleaners are called when everything ends, note that Stop() must be called explicitly.
type Cleaner interface {
	Clean() error
}

type Injector struct {
	// this slice is here because we want to initialize objects in the order as they are added (after the graph is generated):
	c           context.Context
	objects     []*Object
	stopped     bool
	Logger      func(c context.Context, format string, v ...interface{})
	FatalLogger func(c context.Context, format string, v ...interface{})
}

// NewDebug starts a new injector with debug output
func NewDebug() *Injector {
	di := New()
	return di
}

func New() *Injector {
	return &Injector{
		c:       context.Background(),
		objects: []*Object{},
	}
}

func (i *Injector) WithLogger(logger func(c context.Context, format string, v ...interface{}), fatalLogger func(c context.Context, format string, v ...interface{})) *Injector {
	i.Logger = logger
	i.FatalLogger = logger
	return i
}

func (i *Injector) log(c context.Context, format string, v ...interface{}) {
	if i.Logger != nil {
		i.Logger(c, format, v...)
	} else {
		fmt.Printf(format+"\n", v...)
	}
}

func (i *Injector) logAndPanic(c context.Context, format string, v ...interface{}) {
	if i.FatalLogger != nil {
		i.FatalLogger(c, format, v...)
	} else {
		i.log(c, "FATAL: "+format, v...)
	}
	panic(fmt.Sprintf(format, v...))
}

func (i *Injector) WithObjects(objects ...interface{}) *Injector {
	for _, obj := range objects {
		i.WithObject(obj)
	}
	return i
}

func (i *Injector) WithObject(object interface{}) *Injector {
	for _, o := range i.objects {
		if o.Name == "" {
			if reflect.TypeOf(o.Value) == reflect.TypeOf(object) {
				i.logAndPanic(i.c, "Object with type %s already exists", reflect.TypeOf(object).String())
			}
		}
	}
	i.log(i.c, "Adding %T", object)
	o := &Object{Value: object}
	i.objects = append(i.objects, o)
	return i
}

func (i *Injector) WithNamedObject(name string, obj interface{}) *Injector {
	if name == "" {
		i.logAndPanic(i.c, "Named object must have a name")
	}
	for _, o := range i.objects {
		if o.Name != "" {
			if o.Name == name {
				i.logAndPanic(i.c, "Object with name %s already exists", name)
			}
		}
	}
	i.log(i.c, "Adding %s: %T", name, obj)
	o := &Object{Name: name, Value: obj}
	i.objects = append(i.objects, o)
	return i
}

func (i *Injector) AllObjects() []interface{} {
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
func (i Injector) MustGetNamedObject(sample interface{}, name string) interface{} {
	sampleType := reflect.TypeOf(sample)
	if sampleType.Kind() != reflect.Ptr {
		i.logAndPanic(i.c, "Sample must be interface, found %T", sample)
	}
	for _, obj := range i.objects {
		if reflect.TypeOf(obj.Value) == sampleType && obj.Name == name {
			return obj.Value
		}
	}
	i.logAndPanic(i.c, "Object not found: %s.%T", name, sample)
	return nil
}

// MustGetObject: see MustGetNamedObject
func (i Injector) MustGetObject(sample interface{}) interface{} {
	return i.MustGetNamedObject(sample, "")
}

func (i *Injector) getCandidatesForField(obj any, fld reflector.ObjField, tag string) []any {
	var candidates []any
	switch tag {
	case "":
		for m := range i.objects {
			if i.objects[m].Name == "" {
				// fmt.Printf("checking %T.%s (%s) and %T (%s)\n", i.objects[n].Value, fld.Name(), fld.Type().String(), i.objects[m].Value, i.objects[m].Name)
				if reflect.TypeOf(i.objects[m].Value).AssignableTo(fld.Type()) {
					i.log(i.c, "assigning %T.%s (%s) <-> %T (%s)", obj, fld.Name(), fld.Type().String(), i.objects[m].Value, i.objects[m].Name)
					candidates = append(candidates, i.objects[m].Value)
				}
			}
		}
	case "inline":
	default:
		for m := range i.objects {
			if i.objects[m].Name == tag {
				i.log(i.c, "assigning %T.%s (%s) <-> %T (%s)", obj, fld.Name(), fld.Type().String(), i.objects[m].Value, i.objects[m].Name)
				candidates = append(candidates, i.objects[m].Value)
			}
		}
	}
	return candidates
}

func (i *Injector) inject(o any) {
	i.log(i.c, "initializing fields of %T", o)
	obj := reflector.New(o)
fld_loop:
	for _, fld := range obj.Fields() {
		tags, err := fld.Tags()
		if err != nil {
			continue fld_loop
		}
		name, found := tags["inject"]
		if !found {
			continue fld_loop
		}
		if name == "inline" {
			// Recursive for other fields
			i.log(i.c, "initializing inline field %T.%s", o, fld.Name())
			inlineObj := reflect.New(fld.Type()).Interface()
			i.inject(inlineObj)
			fld.Set(reflect.ValueOf(inlineObj).Elem().Interface())
			i.log(i.c, "initialized inline field %T.%s", o, fld.Name())
		} else {
			i.log(i.c, "initializing field %T.%s", o, fld.Name())
			candidates := i.getCandidatesForField(o, fld, name)
			if len(candidates) == 0 {
				i.logAndPanic(i.c, "No candidates for %T.%s (%s)", o, fld.Name(), fld.Type().String())
			}
			if len(candidates) > 1 {
				i.logAndPanic(i.c, "%d candidates (instead of 1) for %T.%s (%s)", len(candidates), o, fld.Name(), fld.Type().String())
			}
			if err := fld.Set(candidates[0]); err != nil {
				i.logAndPanic(i.c, "error setting %T.%s <-> %T", o, fld.Name(), candidates[0])
			}
			i.log(i.c, "initialized field %T.%s", o, fld.Name())
		}
	}
}

// InitializeGraph initializes a graph, but fails if an object is not specified with one of the With() methods.
func (i *Injector) InitializeGraph() *Injector {
	i.log(i.c, "Initializing %d objects", len(i.objects))

	for n := range i.objects {
		i.inject(i.objects[n].Value)
	}

	for _, obj := range i.AllObjects() {
		// TODO: Check that it doesn't depend on an unitialized object
		if initializer, is := obj.(Initializer); is {
			i.log(i.c, "Initializing %T", obj)
			if err := initializer.Init(); err != nil {
				i.logAndPanic(i.c, "Error initializing privided object %T:%s", obj, err.Error())
			}
			i.log(i.c, "Initialized %T", obj)
		}
	}

	return i
}

func (i *Injector) WithCleanBeforeShutdown(maxDuration time.Duration, sig ...os.Signal) *Injector {
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, sig...)
		s := <-c
		fmt.Printf("Got signal %v, cleaning before exit...\n", s.String())
		i.Stop(maxDuration, true)
	}()
	return i
}

func (i *Injector) Stop(maxDuration time.Duration, exit bool) {
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
		i.log(i.c, "all cleaned => exit")
		if exit {
			os.Exit(0)
		}
	}
	i.log(i.c, "NOT all cleaned => exit")
	if exit {
		os.Exit(1)
	}
}

func (i *Injector) cleanCleanable(cleaner Cleaner, maxDuratiDuration time.Duration) error {
	done := make(chan bool)
	errorChan := make(chan error)
	timeoutChan := time.After(maxDuratiDuration)

	go func() {
		i.log(i.c, "Cleaning %T", cleaner)
		defer i.log(i.c, "Cleaned %T", cleaner)
		if err := cleaner.Clean(); err != nil {
			msg := fmt.Sprintf("Error cleaning %T: %+v", cleaner, err)
			i.log(i.c, msg)
			fmt.Fprintln(os.Stderr, msg)
			errorChan <- err
		}
		done <- true
	}()

	for {
		select {
		case err := <-errorChan:
			return err
		case <-done:
			return nil
		case <-timeoutChan:
			msg := fmt.Sprintf("Cleaning %T took more than %s", cleaner, maxDuratiDuration.String())
			fmt.Fprintln(os.Stderr, msg)
			i.log(i.c, msg)
			return errors.New(msg)
		case <-time.After(time.Second):
			i.log(i.c, "Still cleaning %T\n", cleaner)
		}
	}
}

func (i *Injector) Stopper(maxDuration time.Duration, exitAfterStop bool) func() {
	return func() {
		i.Stop(maxDuration, exitAfterStop)
	}
}
