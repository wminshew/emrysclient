package cmd

import (
	"errors"
	"fmt"
	"github.com/wminshew/emrys/pkg/validate"
	"log"
	"path/filepath"
)

type jobReq struct {
	project      string
	requirements string
	main         string
	data         string
	output       string
}

func (j *jobReq) validate() error {
	if j.project == "" {
		return errors.New("must specify a project in config or with flag")
	}
	projectRegexp := validate.ProjectRegexp()
	if !projectRegexp.MatchString(j.project) {
		return fmt.Errorf("project (%s) must satisfy regex constraints: %s", j.project, projectRegexp)
	}
	if j.main == "" {
		return errors.New("must specify a main execution file in config or with flag")
	}
	if j.requirements == "" {
		return errors.New("must specify a requirements file in config or with flag")
	}
	if j.output == "" {
		return errors.New("must specify an output directory in config or with flag")
	}
	if j.data == j.output {
		return errors.New("can't use same directory for data and output")
	}
	if filepath.Base(j.data) == "output" {
		return errors.New("can't name data directory \"output\"")
	}
	if j.data != "" {
		if filepath.Dir(j.main) != filepath.Dir(j.data) {
			return fmt.Errorf("main (%v) and data (%v) must be in the same directory", j.main, j.data)
		}
	}
	if filepath.Dir(j.main) != filepath.Dir(j.output) {
		log.Printf("Warning! Main (%v) will still only be able to save locally to "+
			"./output when executing, even though output (%v) has been set to a different "+
			"directory. Local output to ./output will be saved to your output (%v) at the end "+
			"of execution. If this is your intended workflow, please ignore this warning.\n",
			j.main, j.output, j.output)
	}
	return nil
}
