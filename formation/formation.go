package formation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/convox/kernel/Godeps/_workspace/src/github.com/awslabs/aws-sdk-go/aws"
	"github.com/convox/kernel/Godeps/_workspace/src/github.com/awslabs/aws-sdk-go/service/sqs"
)

var MessageQueueUrl = os.Getenv("FORMATION_QUEUE")

type Message struct {
	MessageID     *string
	ReceiptHandle *string

	Type             string
	MessageId        string
	TopicArn         string
	Subject          string
	Message          string
	Timestamp        time.Time
	SignatureVersion string
	Signature        string
	SigningCertURL   string
	UnsubscribeURL   string
}

type Request struct {
	ResourceType string
	RequestType  string

	RequestId          string
	StackId            string
	LogicalResourceId  string
	PhysicalResourceId string
	ResponseURL        string

	ResourceProperties map[string]interface{}
}

type Response struct {
	RequestId         string
	StackId           string
	LogicalResourceId string

	Data               map[string]string
	PhysicalResourceId string
	Reason             string
	Status             string
}

func Listen() {
	for {
		messages, err := dequeueMessage()

		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			continue
		}

		if len(messages) == 0 {
			continue
		}

		for _, message := range messages {
			if message.Subject == "AWS CloudFormation custom resource request" {
				handleFormation(message)
			}
		}

		num, err := ackMessage(messages)

		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			continue
		}

		fmt.Printf("success: messages=%d\n", num)
	}
}

func handleFormation(message Message) {
	var freq Request

	err := json.Unmarshal([]byte(message.Message), &freq)

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return
	}

	physical := ""

	switch freq.ResourceType {
	case "Custom::ECSCluster":
		physical, err = HandleECSCluster(freq)
	case "Custom::ECSService":
		physical, err = HandleECSService(freq)
	case "Custom::ECSTaskDefinition":
		physical, err = HandleECSTaskDefinition(freq)
	case "Custom::LambdaFunction":
		physical, err = HandleLambdaFunction(freq)
	default:
		physical = ""
		err = fmt.Errorf("unknown ResourceType: %s", freq.ResourceType)
	}

	fres := Response{
		RequestId:          freq.RequestId,
		StackId:            freq.StackId,
		LogicalResourceId:  freq.LogicalResourceId,
		PhysicalResourceId: physical,
		Status:             "SUCCESS",
		Data: map[string]string{
			"Output1": "Value1",
		},
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		fres.Reason = err.Error()
		fres.Status = "FAILED"
	}

	data, err := json.Marshal(fres)

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return
	}

	req, _ := http.NewRequest("PUT", "", bytes.NewBuffer(data))

	// golang's http methods munge the %3A in amazon urls so we build it manually using Opaque
	rurl := freq.ResponseURL
	parts := strings.SplitN(rurl, "/", 4)
	req.URL.Scheme = parts[0][0 : len(parts[0])-1]
	req.URL.Host = parts[2]
	req.URL.Opaque = fmt.Sprintf("//%s/%s", parts[2], parts[3])

	client := &http.Client{}

	res, err := client.Do(req)

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return
	}

	rr, _ := ioutil.ReadAll(res.Body)

	fmt.Printf("string(rr) %+v\n", string(rr))
}

func dequeueMessage() ([]Message, error) {
	req := &sqs.ReceiveMessageInput{
		MaxNumberOfMessages: aws.Long(10),
		QueueURL:            aws.String(MessageQueueUrl),
		WaitTimeSeconds:     aws.Long(10),
	}

	res, err := SQS().ReceiveMessage(req)

	if err != nil {
		return nil, err
	}

	messages := make([]Message, len(res.Messages))

	var message Message

	for i, m := range res.Messages {
		err = json.Unmarshal([]byte(*m.Body), &message)

		if err != nil {
			return nil, err
		}

		message.MessageID = m.MessageID
		message.ReceiptHandle = m.ReceiptHandle

		messages[i] = message
	}

	return messages, nil
}

func ackMessage(messages []Message) (int, error) {
	dreq := &sqs.DeleteMessageBatchInput{
		QueueURL: aws.String(MessageQueueUrl),
	}

	dreq.Entries = make([]*sqs.DeleteMessageBatchRequestEntry, len(messages))

	for i, message := range messages {
		dreq.Entries[i] = &sqs.DeleteMessageBatchRequestEntry{
			ID:            message.MessageID,
			ReceiptHandle: message.ReceiptHandle,
		}
	}

	_, err := SQS().DeleteMessageBatch(dreq)

	return len(messages), err
}
