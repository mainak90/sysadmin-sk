/*
 * This file is part of the Sysadmin Sidekick Toolkit (Sysadmin-SK) (https://github.com/raffs/sysadmin-sk).
 * Copyright (c) 2019 Rafael Oliveira Silva
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, version 3.
 *
 * This program is distributed in the hope that it will be useful, but
 * WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU
 * General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */
package sqs

import (
    "fmt"
    "strconv"

    "github.com/aws/aws-sdk-go/aws"
    "github.com/aws/aws-sdk-go/aws/session"
    "github.com/aws/aws-sdk-go/service/sqs"
)

type MoveMessageOptions struct {

    // Define the Source Queue Name that will be move the message from.
    SourceQueueName           string    `type:"string" required:"true"`

    // Define the target queue where the message will be moved to.
    TargetQueueName           string    `type:"string" required:"true"`

    // Define the maximum number of messages to be processed at a time.
    MaxNumberOfMessages       int64     `type:"int64" required:"false"`

    // TODO: Proper documentation of this here, please :)
    WaitTimeSeconds           int64     `type:"int64" required:"false"`

    // Define the message visibility when ingesting the message to the target queue.
    VisibilityTimeout         int64     `type:"int64" required:"false"`

    // Define a regex string to filter the message body.
    FilterString              string    `type:"string" required:"false"`

    // Whether to delete a message from the source queue. default: false
    KeepMessageOnSourceQueue  bool   `type:"string" required:"false"`

    // Define message attributes to filter which messages should be ingested
    // into the target queue.
    FilterAttributes          string    `type:"string" required:"false"`

    // Define the AWS Region to connect to. This essentially will be converted
    // to an URL with the region name.
    AwsRegion                 string     `type:"string" required:"false"`

    // In case you want to overwrite the underlying endpoint for testing or
    // other kind black Sorcery.
    AwsEndpoint               string     `type:"string" required:"false"`

    // Define the AWS profile
    AwsProfile               string     `type:"string" required:"false"`
}

/**
 * Given a queue name return the URL. Just a wrapper because we need to use twice.
 */
func getQueueUrl(client *sqs.SQS, queueName *string) (*sqs.GetQueueUrlOutput) {
    queue, err := client.GetQueueUrl(&sqs.GetQueueUrlInput{
        QueueName: aws.String(*queueName),
    })

    if err != nil {
        panic(err)
    }

    return queue
}

/**
 * Given the queue URL return the Queue attributes which include queue type, ARN and
 * more important the approximate number of messages at the moment.
 */
func getQueueAttributes(client *sqs.SQS, queueUrl *string) (*sqs.GetQueueAttributesOutput) {
    queue, err := client.GetQueueAttributes(&sqs.GetQueueAttributesInput{
        QueueUrl: aws.String(*queueUrl),
        AttributeNames: aws.StringSlice([]string{"All"}),
    })

    if err != nil {
        panic(err)
    }

    return queue
}

/**
 * Given a MoveMessageOptions struct with the proper source and target queue
 * along with additional options for fine control migration. And sync and/or move
 * all or the partially (see filters options) from source queue to target queue.
 */
func MoveMessages(options *MoveMessageOptions) (error) {

    sessionOpts := session.Options{
        SharedConfigState: session.SharedConfigEnable,

        // aws configuration options
        Config: aws.Config{
            Region: aws.String(options.AwsRegion),
            Endpoint: aws.String(options.AwsEndpoint),
        },
    }

    session, err := session.NewSessionWithOptions(sessionOpts)
    if err != nil {
        panic(err)
    }

    client := sqs.New(session)

    // get Queue's url and related attributes
    sourceQueue := getQueueUrl(client, &options.SourceQueueName)
    targetQueue := getQueueUrl(client, &options.TargetQueueName)

    sourceQueueAttr := getQueueAttributes(client, sourceQueue.QueueUrl)
    targetQueueAttr := getQueueAttributes(client, targetQueue.QueueUrl)

    sourceNumMessages, err := strconv.Atoi(*sourceQueueAttr.Attributes["ApproximateNumberOfMessages"])
    if err != nil {
        panic(err)
    }

    targetNumMessages, err := strconv.Atoi(*targetQueueAttr.Attributes["ApproximateNumberOfMessages"])
    if err != nil {
        panic(err)
    }

    // if there's no message, our job is done here, let's pack it and go home
    if sourceNumMessages <= 0 {
        fmt.Println(fmt.Sprintf("No messages in Queue: '%s'", *sourceQueue.QueueUrl))
        fmt.Println("No actions to be done here partner")
        return nil
    }

    // Displaying summary of queues
    fmt.Printf("Source Queue '%s' contains %d of messages\n", options.SourceQueueName, sourceNumMessages)
    fmt.Printf("Target Queue '%s' contains %d of messages\n", options.TargetQueueName, targetNumMessages)

    messageInOptions := &sqs.ReceiveMessageInput{
        QueueUrl: sourceQueue.QueueUrl,
        MaxNumberOfMessages: aws.Int64(options.MaxNumberOfMessages),
        WaitTimeSeconds: aws.Int64(options.WaitTimeSeconds),
        VisibilityTimeout: aws.Int64(options.VisibilityTimeout),
        MessageAttributeNames: []*string{aws.String(sqs.QueueAttributeNameAll)},
        AttributeNames: []*string{
            aws.String(sqs.MessageSystemAttributeNameSentTimestamp),
        },
    }

    // Initialize the failed and succeed message counter
    sendFailedMsgs := int64(0)
    sendSuccessMsgs := int64(0)
    deleteFailedMsgs := int64(0)
    deleteSuccessMsgs := int64(0)

    // loop over all the message until we are done.
    for {
        var sendBatchMessages []*sqs.SendMessageBatchRequestEntry
        var deleteBatchMessages []*sqs.DeleteMessageBatchRequestEntry

        messageIn, err := client.ReceiveMessage(messageInOptions)
        if err != nil {
            panic(err)
        }

        if len(messageIn.Messages) <= 0 {
            break
        }

        for _, message := range messageIn.Messages {

            // append the message to batchMessages to be send in batch
            mRequest := sqs.SendMessageBatchRequestEntry {
                MessageAttributes: message.MessageAttributes,
                MessageBody: message.Body,
                Id: message.MessageId,
            }

            sendBatchMessages = append(sendBatchMessages, &mRequest)

            // append the message to the delete list
            dRequest := sqs.DeleteMessageBatchRequestEntry{Id: message.MessageId, ReceiptHandle: message.ReceiptHandle}
            deleteBatchMessages = append(deleteBatchMessages, &dRequest)
        }

        batchSendMessagesInput := &sqs.SendMessageBatchInput{
            QueueUrl: targetQueue.QueueUrl,
            Entries: sendBatchMessages,
        }

        sendResult, err := client.SendMessageBatch(batchSendMessagesInput)
        if err != nil {
            fmt.Println("Failed to send the message to target queue in batch mode")
            fmt.Println("We should abort this, as a sense something is wrong")
            panic(err)
        }

        sendSuccessMsgs += int64(len(sendResult.Successful))
        sendFailedMsgs += int64(len(sendResult.Failed))

        fmt.Printf(".")

        // Delete successfully migrated message from source queue
        if !options.KeepMessageOnSourceQueue && sendSuccessMsgs > 0 {

            // no succeed message to be deleted.
            if len(deleteBatchMessages) <= 0 {
                continue
            }

            batchDeleteMessagesInput := &sqs.DeleteMessageBatchInput{
                QueueUrl: sourceQueue.QueueUrl,
                Entries: deleteBatchMessages,
            }

            deleteResult, err := client.DeleteMessageBatch(batchDeleteMessagesInput)
            if err != nil {
                fmt.Println("Failed to send the message to target queue in batch mode")
                fmt.Println("We should abort this, as a sense something is wrong")
                panic(err)
            }

            deleteSuccessMsgs += int64(len(deleteResult.Successful))
            deleteFailedMsgs += int64(len(deleteResult.Failed))

            fmt.Printf(".")
        }
    }

    fmt.Printf("\n\nSummary\n")
    fmt.Printf("Migrated: %d successfully from source queue to target queue\n", sendSuccessMsgs)
    fmt.Printf("Successfully sync/move all the messages, my done job is done here partner!\n")
    return nil   // return null because, there is no error to be return.
}