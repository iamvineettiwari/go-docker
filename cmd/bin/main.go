package main

import (
	"log"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/iamvineettiwari/go-docker/internals"
	"github.com/joho/godotenv"
)

func createChild(args []string) {
	cli := exec.Command("/proc/self/exe", args...)
	cli.Stdin = os.Stdin
	cli.Stdout = os.Stdout
	cli.Stderr = os.Stderr
	cli.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS |
			syscall.CLONE_NEWPID |
			syscall.CLONE_NEWNS |
			syscall.CLONE_NEWUSER,
	}
	cli.SysProcAttr.UidMappings = []syscall.SysProcIDMap{
		{
			ContainerID: 0,
			HostID:      os.Getuid(),
			Size:        1,
		},
	}
	cli.SysProcAttr.GidMappings = []syscall.SysProcIDMap{
		{
			ContainerID: 0,
			HostID:      os.Getgid(),
			Size:        1,
		},
	}

	err := cli.Run()

	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	err := godotenv.Load()

	if err != nil {
		log.Fatal(err.Error())
	}

	args := os.Args[1:]

	command := args[0]

	if command == "run" {

		image := args[1]

		clientId := os.Getenv("CLIENT_ID")
		username := os.Getenv("DOCKER_USERNAME")
		password := os.Getenv("DOCKER_PASSWORD")

		dockerManager := internals.NewDockerManager(clientId, username, password)
		basePath, imageConfig, err := dockerManager.PullImage(image)

		if err != nil {
			log.Fatal(err.Error())
		}

		baseData := []string{}
		baseData = append(baseData, basePath)
		baseData = append(baseData, strconv.Itoa(len(imageConfig.Env)))
		baseData = append(baseData, imageConfig.Env...)
		baseData = append(baseData, imageConfig.WorkingDir)

		if len(args[2:]) > 0 {
			baseData = append(baseData, args[2:]...)
		} else {
			baseData = append(baseData, imageConfig.Cmd...)
		}

		createChild(baseData)
	} else {
		basePath := args[0]
		envLength, err := strconv.Atoi(args[1])

		if err != nil {
			panic(err)
		}

		env := []string{}

		argsPointer, currentEnv := 2, 0

		if envLength > 0 {
			for currentEnv < envLength {
				env = append(env, args[argsPointer])
				argsPointer++
				currentEnv++
			}
		}

		workingDir := args[argsPointer]
		argsPointer++

		args := args[argsPointer:]

		client := internals.NewClient(basePath, workingDir, env)
		client.Run(args)
	}
}
