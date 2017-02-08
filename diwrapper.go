package diwrapper

import (
	"fmt"
	"os"

	"github.com/facebookgo/inject"
)

type Initializer interface {
	Init() error
}

// Callers are cllaed when everything ends, note that Stop() must be called explicitly.
type Cleaner interface {
	Clean() error
}

type InjectWrapper map[string]*inject.Object

func New() *InjectWrapper {
	i := InjectWrapper(make(map[string]*inject.Object))
	return &i
}

func (i *InjectWrapper) WithObjects(objects ...interface{}) *InjectWrapper {
	for _, obj := range objects {
		i.WithObject(obj)
	}
	return i
}

func (i *InjectWrapper) WithObject(object interface{}) *InjectWrapper {
	(*i)[fmt.Sprintf("____%d____", len(*i))] = &inject.Object{Value: object}
	return i
}

func (i *InjectWrapper) WithNamedObject(name string, obj interface{}) *InjectWrapper {
	if _, found := (*i)[name]; found {
		panic(fmt.Sprintf("Double object with name %s", name))
	}
	(*i)[name] = &inject.Object{Name: name, Value: obj}
	return i
}

func (i *InjectWrapper) AllObjects() []interface{} {
	res := []interface{}{}
	for _, diObj := range *i {
		res = append(res, diObj.Value)
	}
	return res
}

func (i *InjectWrapper) InitializeGraph() *InjectWrapper {
	var g inject.Graph
	for name, diObj := range *i {
		if err := g.Provide(diObj); err != nil {
			panic(fmt.Sprintf("Error providing object %s.%T:%s", name, diObj.Value, err.Error()))
		}
	}
	if err := g.Populate(); err != nil {
		panic(fmt.Sprintf("Error populating graph: %s", err))
	}
	for _, obj := range i.AllObjects() {
		if initializer, is := obj.(Initializer); is {
			if err := initializer.Init(); err != nil {
				panic(fmt.Sprintf("Error initializing privided object %T:%s", obj, err.Error()))
			}
		}
	}
	return i
}

func (i *InjectWrapper) Stop() {
	for _, obj := range i.AllObjects() {
		if cleaner, is := obj.(Cleaner); is {
			if err := cleaner.Clean(); err != nil {
				fmt.Fprintf(os.Stderr, "Error cleaning %T: %+v\n", obj, err)
			}
		}
	}
}

func (i *InjectWrapper) Stopper() func() {
	return func() {
		i.Stop()
	}
}
