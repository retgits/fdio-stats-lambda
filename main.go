// Package main is the main executable of the serverless function. It will create a stats report
// and create a Trello card with the result. This app reuses the code from
// [fdio](https://github.com/retgits/fdio) and wraps it in a Lambda function
package main

// The imports
import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"github.com/aws/aws-lambda-go/events"
	rt "github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/lambda"
	"github.com/retgits/fdio/database"
	"github.com/retgits/fdio/util"
)

// Variables
var (
	// The region in which the Lambda function is deployed
	awsRegion = util.GetEnvKey("region", "us-west-2")
	// The name of the bucket that has the csv file
	s3Bucket = util.GetEnvKey("s3Bucket", "retgits-fdio")
	// The temp folder to store the database file while working
	tempFolder = util.GetEnvKey("tempFolder", ".")
)

// Constants
const (
	// The name of the FDIO database file
	databaseName = "fdiodb.db"
	// The name of the temporary data file
	tempFile = "temp.txt"
	// The name of the Trello ARN parameter in Amazon SSM
	trelloArnName = "/trello/arn"
)

// LambdaEvent is the outer structure of the events that are received by this function
type LambdaEvent struct {
	EventVersion string
	EventSource  string
	Event        interface{}
}

// TrelloEvent is the structure for the data representing a TrelloCard
type TrelloEvent struct {
	Title       string
	Description string
}

// The handler function is executed every time that a new Lambda event is received.
// It takes a JSON payload (you can see an example in the event.json file) and only
// returns an error if the something went wrong. The event comes fom CloudWatch and
// is scheduled every interval (where the interval is defined as variable)
func handler(request events.CloudWatchEvent) error {
	log.Printf("Processing Lambda request [%s]", request.ID)

	// Create a new session without AWS credentials. This means the Lambda function must have
	// privileges to read and write S3
	awsSession := session.Must(session.NewSession(&aws.Config{
		Region: aws.String(awsRegion),
	}))

	// Get the Trello ARN
	trelloARN, err := util.GetSSMParameter(awsSession, trelloArnName, true)
	if err != nil {
		return err
	}
	fmt.Println(trelloARN)

	// Download the latest version of the FDIO database from S3
	err = util.DownloadFile(awsSession, tempFolder, databaseName, s3Bucket)
	if err != nil {
		log.Println(err.Error())
		return err
	}

	// Get a file handle to the FDIO database
	db, err := database.New(filepath.Join(tempFolder, databaseName), false)
	if err != nil {
		log.Printf("Error while connecting to the database: %s\n", err.Error())
		return err
	}

	// Create a temp file
	file, err := os.OpenFile(filepath.Join(tempFolder, tempFile), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		log.Printf("Error while creating temp text file: %s\n", err.Error())
	}

	// Set default queryOpts
	queryOpts := database.QueryOptions{
		Writer:     file,
		Query:      "",
		MergeCells: true,
		RowLine:    true,
		Render:     true,
	}

	// Execute the stats queries
	file.WriteString(fmt.Sprintf("The Flogo community has built a ton of things!\n```\n"))
	queryOpts.Query = "select type, count(type) as num from acts group by type"
	_, err = db.RunQuery(queryOpts)
	if err != nil {
		log.Printf("Error while executing query: %s\n", err.Error())
	}

	file.WriteString(fmt.Sprintf("```\n\nLooking at the community we have\n```\n"))
	queryOpts.Query = "SELECT COUNT(DISTINCT author) as Users FROM acts"
	_, err = db.RunQuery(queryOpts)
	if err != nil {
		log.Printf("Error while executing query: %s\n", err.Error())
	}

	file.WriteString(fmt.Sprintf("```\n\nLooking at the people that do not have \"TIBCO\" in their name (_but they still could be employees..._)\n```\n"))
	queryOpts.Query = "SELECT COUNT(DISTINCT author) as 'Users' FROM acts WHERE author NOT LIKE '%TIBCO%' COLLATE NOCASE"
	_, err = db.RunQuery(queryOpts)
	if err != nil {
		log.Printf("Error while executing query: %s\n", err.Error())
	}

	file.WriteString(fmt.Sprintf("```\n\nThe Flogo Leaderboard:\n```\n"))
	queryOpts.Query = "select author, count(author) as num from acts group by author order by num desc limit 5"
	_, err = db.RunQuery(queryOpts)
	if err != nil {
		log.Printf("Error while executing query: %s\n", err.Error())
	}

	file.WriteString(fmt.Sprintf("```\n\nIf we remove the \"Unknown\" contributions and contributions from people that identify as \"TIBCO Software Inc.\" the leaderboard is:\n```\n"))
	queryOpts.Query = "select author, count(author) as num from acts where author not in ('Unknown','Your Name <you.name@example.org>') and author not like 'TIBCO Software%' group by author order by num desc limit 5"
	_, err = db.RunQuery(queryOpts)
	if err != nil {
		log.Printf("Error while executing query: %s\n", err.Error())
	}

	file.WriteString(fmt.Sprintf("```\n\n"))

	// Read the file
	fileBytes, err := ioutil.ReadFile(filepath.Join(tempFolder, tempFile))
	if err != nil {
		log.Printf("Error while reading temp file: %s\n", err.Error())
	}

	// Send the details to the Trello Lambda function
	trelloEvent := TrelloEvent{
		Description: string(fileBytes),
		Title:       "Weekly stats for Flogo",
	}
	payload := LambdaEvent{
		EventVersion: "1.0",
		EventSource:  "aws:lambda",
		Event:        trelloEvent,
	}

	var b []byte
	b, _ = json.Marshal(payload)

	// Execute the call to the Trello Lambda function
	lambdaSession := lambda.New(awsSession)
	_, err = lambdaSession.Invoke(&lambda.InvokeInput{
		FunctionName: &trelloARN,
		Payload:      b})

	if err != nil {
		log.Printf("Error while invoking Lambda function: %s", err.Error())
		return err
	}

	os.Remove(filepath.Join(tempFolder, tempFile))
	db.Close()

	return nil
}

// The main method is executed by AWS Lambda and points to the handler
func main() {
	rt.Start(handler)
}
