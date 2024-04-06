package internals

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

type Client struct {
	basePath   string
	env        []string
	workingDir string
}

func NewClient(basePath, workingDir string, env []string) *Client {
	return &Client{
		basePath:   basePath,
		env:        env,
		workingDir: workingDir,
	}
}

func (c *Client) chroot() {
	chRootPath := filepath.Join("./", c.basePath)

	if err := syscall.Chroot(chRootPath); err != nil {
		log.Fatal("Error:", err)
	}

	if err := syscall.Chdir("/"); err != nil {
		log.Fatal("Error changing directory:", err)
	}
}

func (c *Client) mount() {
	if err := os.MkdirAll("proc", 0777); err != nil {
		log.Fatal("Proc File err : ", err)
	}

	if err := syscall.Mount("proc", "proc", "proc", 0, ""); err != nil {
		log.Fatal("Error mounting proc:", err)
	}

	if err := syscall.Unshare(syscall.CLONE_NEWNS | syscall.CLONE_NEWPID); err != nil {
		log.Fatal("Error creating new mount namespace:", err)
	}
}

func (c *Client) unmount() {
	if err := syscall.Unmount("/proc", 0); err != nil {
		log.Fatal(err)
	}
}

func (c *Client) Run(args []string) {
	namespace := "container"

	syscall.Sethostname([]byte(namespace))

	c.chroot()
	c.mount()

	defer c.unmount()

	if len(args) > 0 {
		command := args[0]
		args = args[1:]

		cli := exec.Command(command, args...)
		cli.Stdin = os.Stdin
		cli.Stdout = os.Stdout
		cli.Stderr = os.Stderr

		cli.Env = c.env
		cli.Dir = c.workingDir

		err := cli.Run()

		if err != nil {
			log.Fatal(err)
		}
	}
}

func DoesNotExists(path string) bool {
	if _, err := os.Stat(path); err != nil {
		return os.IsNotExist(err)
	}

	return false
}
