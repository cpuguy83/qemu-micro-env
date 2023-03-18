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
