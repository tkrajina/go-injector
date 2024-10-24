package main

import (
	"fmt"
	"time"

	"github.com/tkrajina/go-injector/injector"
)

type EmailSender struct {
	Queue []string
}

func (es *EmailSender) Init() error {
	fmt.Println("Email sender initialized")
	go func() {
		for {
			if len(es.Queue) > 0 {
				var first string
				first, es.Queue = es.Queue[0], es.Queue[1:]
				fmt.Println("Sending ", first, "queue=", es.Queue)
			}
			time.Sleep(time.Second)
		}
	}()
	return nil
}

func (es *EmailSender) Clean() error {
	fmt.Println("starting cleanup")
	for len(es.Queue) > 0 {
		fmt.Printf("Email queue still not empty (%d)\n", len(es.Queue))
		time.Sleep(time.Second)
	}
	fmt.Println("finished cleaning")
	return nil
}

func main() {
	emailSender := new(EmailSender)
	injector.New().
		WithObject(emailSender).
		WithCleanBeforeShutdown(3 * time.Second).
		InitializeGraph()

	for i := 0; i < 10; i++ {
		emailSender.Queue = append(emailSender.Queue, fmt.Sprintf("email %d", i))
	}
	time.Sleep(time.Hour)
}
