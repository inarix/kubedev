package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gobuffalo/packr"
	"github.com/kubedev/models"
	"github.com/kubedev/utils"
	"github.com/relferreira/sse"
	cors "github.com/rs/cors/wrapper/gin"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	r := gin.Default()
	var kubeconfig = flag.String("kubeconfig", filepath.Join(os.Getenv("HOME"), ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	flag.Parse()
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		panic(err.Error())
	}

	clientset, err := kubernetes.NewForConfig(config)

	r.Use(cors.New(cors.Options{
		AllowedOrigins:     []string{"http://localhost:1234"},
		AllowedMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:     []string{"Authorization", "Accept", "Origin", "Content-Type"},
		AllowCredentials:   true,
		OptionsPassthrough: false,
		Debug:              true,
	}))

	box := packr.NewBox("./dist")
	// r.StaticFS("/", box)
	r.Use(utils.Serve("/", box))
	r.NoRoute(utils.RedirectIndex())

	r.GET("/api/:namespace/exec", func(c *gin.Context) {
		namespace := c.Param("namespace")
		command := strings.Fields(c.Query("command"))
		jsonOutput, jsonErr := strconv.ParseBool(c.Query("json"))
		if jsonErr != nil {
			panic(err.Error())
		}

		fullCommand := []string{}

		fullCommand = append(fullCommand, command...)

		if namespace == "all-namespaces" {
			fullCommand = append(fullCommand, "--all-namespaces")
		} else {
			fullCommand = append(fullCommand, "-n")
			fullCommand = append(fullCommand, namespace)
		}

		if jsonOutput {
			fullCommand = append(fullCommand, "-o")
			fullCommand = append(fullCommand, "json")
		}
		fmt.Printf("%#v\n", fullCommand)
		cmd := exec.Command("kubectl", fullCommand...)

		out, err := cmd.Output()
		output := string(out[:])

		if err != nil {
			panic(err.Error())
		}

		if jsonOutput {
			jsonMesage := json.RawMessage(output)
			c.JSON(200, jsonMesage)
		} else {
			c.JSON(200, nil)
		}

	})

	r.POST("/api/:namespace/apply", func(c *gin.Context) {
		namespace := c.Param("namespace")

		var apply models.ApplyRequest
		errJSON := c.BindJSON(&apply)
		if errJSON != nil {
			panic(errJSON.Error())
		}
		// body := c.Request.Body
		// json, _ := ioutil.ReadAll(body)

		// fmt.Printf("%s \n", string(json))

		filename := ".files/" + strconv.FormatInt(time.Now().Unix(), 10) + ".yaml"
		err = ioutil.WriteFile(filename, []byte(apply.Yaml), 0755)
		if err != nil {
			panic(err.Error())
		}

		fullCommand := []string{}
		fullCommand = append(fullCommand, "-n")
		fullCommand = append(fullCommand, namespace)
		fullCommand = append(fullCommand, "apply")
		fullCommand = append(fullCommand, "-f")
		fullCommand = append(fullCommand, filename)

		cmd := exec.Command("kubectl", fullCommand...)

		_, err := cmd.Output()

		if err != nil {
			panic(err.Error())
		}

		c.JSON(200, nil)
	})

	r.GET("/api/:namespace/pods/:name/:container/logs", func(c *gin.Context) {
		namespace := c.Param("namespace")
		name := c.Param("name")
		logOptions := v1.PodLogOptions{}
		req := clientset.CoreV1().Pods(namespace).GetLogs(name, &logOptions)
		podLogs, err := req.Stream()
		if err != nil {
			panic(err.Error())
		}

		defer podLogs.Close()
		buf := new(bytes.Buffer)
		_, err = io.Copy(buf, podLogs)
		if err != nil {
			panic("error in copy information from podLogs to buf")
		}
		str := buf.String()
		c.JSON(200, gin.H{
			"log": str,
		})
	})

	r.GET("/api/:namespace/pods/:name/:container/logs/stream", func(c *gin.Context) {
		namespace := c.Param("namespace")
		name := c.Param("name")
		container := c.Param("container")
		logOptions := v1.PodLogOptions{
			Container: container,
			Follow:    true,
		}
		req := clientset.CoreV1().Pods(namespace).GetLogs(name, &logOptions)
		podLogs, err := req.Stream()
		if err != nil {
			panic(err.Error())
		}

		defer podLogs.Close()
		chanStream := make(chan string)
		go func() {
			defer close(chanStream)
			reader := bufio.NewReader(podLogs)
			for {
				line, error := reader.ReadBytes('\n')
				if error != nil {
					break
				}
				chanStream <- string(line)
			}
		}()
		c.Stream(func(w io.Writer) bool {
			if msg, ok := <-chanStream; ok {
				c.Render(-1, sse.Event{
					Data: map[string]interface{}{
						"date":    time.Now().Unix(),
						"content": msg,
					},
				})
				return true
			}
			return false
		})
	})

	r.Run(":9898") // listen and serve on 0.0.0.0:8080
}
