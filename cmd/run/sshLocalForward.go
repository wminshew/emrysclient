package run

import (
	"context"
	"fmt"
	"os/exec"
)

func (j *userJob) sshLocalForward(ctx context.Context, sshKeyFile string) error {
	// ssh -v -i {id_rsa} -N -L 8888:/home/{jID}/notebook.sock -p 2222 {jID}@notebook.emrys.io -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null
	cmdStr := "ssh"
	// TODO: change port? what if running notebooks for two different people?
	// TODO: consider using unix domain sockets inside /home/emrys/.config/{jID} ?
	// TODO: make local port settable by user
	args := []string{"-q", "-i", sshKeyFile, "-N", "-L", fmt.Sprintf("8888:/home/%s/notebook.sock", j.id), "-p", "2222", fmt.Sprintf("%s@notebook.emrys.io", j.id), "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null"}
	cmd := exec.CommandContext(ctx, cmdStr, args...)
	return cmd.Run()
}
