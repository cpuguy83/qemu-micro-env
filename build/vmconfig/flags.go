package vmconfig

import (
	"fmt"
	"strconv"
	"strings"
)

type intListFlag []int

func (f *intListFlag) String() string {
	return fmt.Sprint(*f)
}

func (f *intListFlag) Set(s string) error {
	for _, v := range strings.Split(s, ",") {
		i, err := strconv.Atoi(v)
		if err != nil {
			return err
		}
		*f = append(*f, i)
	}
	return nil
}

type stringListFlag []string

func (f *stringListFlag) String() string {
	return fmt.Sprint(*f)
}

func (f *stringListFlag) Set(s string) error {
	*f = append(*f, s)
	return nil
}

type socketListFlag []string

func (f *socketListFlag) String() string {
	return fmt.Sprint(*f)
}

func (f *socketListFlag) Set(s string) error {
	*f = append(*f, strings.Split(s, ",")...)
	return nil
}
