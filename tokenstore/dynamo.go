package tokenstore

import (
	"context"
	"fmt"
	"log"
	"time"

	cache "github.com/alexions/go-lru-ttl-cache"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go/aws"
	cfg "github.com/weberr13/twitchAPILambda/config"
)

var (
	tokenCache *cache.LRUCache
	tokens     *dynamodb.Client
	ourConfig  *cfg.Configuration
)

// TokenDB describes the table structure of stored tokens
type TokenDB struct {
	Name      string `json:"name" dynamodbav:"name"`
	Token     string `json:"token" dynamodbav:"token"`
	ChannelID int    `json:"channelId" dynamodbav:"channelid"`
}

// Init the dynamo connection
func Init(c *cfg.Configuration) {
	sdkConfig, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		log.Fatal(err)
	}

	tokens = dynamodb.NewFromConfig(sdkConfig)
	cfg := cache.Configuration().SetMaxSize(1024).SetDefaultTTL(30 * time.Minute)
	tokenCache = cache.NewLRUCache(cfg)
	ourConfig = c
}

// PutToken info in dynamo and cache
func PutToken(ctx context.Context, name, token string, channelID int) error {
	todo := TokenDB{
		Name:      name,
		Token:     token,
		ChannelID: channelID,
	}

	item, err := attributevalue.MarshalMap(todo)
	if err != nil {
		return err
	}

	input := &dynamodb.PutItemInput{
		TableName: aws.String(ourConfig.TableName),
		Item:      item,
	}

	_, err = tokens.PutItem(ctx, input)
	if err != nil {
		return err
	}
	log.Printf("caching token for %s %d", name, channelID)
	tokenCache.Set(fmt.Sprintf("%s:%d", name, channelID), token)
	return nil
}

// DeleteToken info from dynamo and cache
func DeleteToken(ctx context.Context, name string, channel int) {
	key, err := attributevalue.Marshal(name)
	if err != nil {
		log.Printf("failed to delete stale token %s", err)
		return
	}
	id, err := attributevalue.Marshal(channel)
	if err != nil {
		log.Printf("failed to delete stale token %s", err)
		return
	}

	input := &dynamodb.DeleteItemInput{
		TableName: aws.String(ourConfig.TableName),
		Key: map[string]types.AttributeValue{
			"name":      key,
			"channelid": id,
		},
		ReturnValues: types.ReturnValue(*aws.String("ALL_OLD")),
	}

	_, err = tokens.DeleteItem(ctx, input)
	if err != nil {
		log.Printf("failed to delete stale token %s", err)
		return
	}
	tokenCache.Delete(fmt.Sprintf("%s:%d", name, channel))
}

// GetToken info from dynamo and cache
func GetToken(ctx context.Context, name string, channel int) string {
	v, ok := tokenCache.Get(fmt.Sprintf("%s:%d", name, channel))
	if ok {
		token, ok := v.(string)
		if ok {
			log.Printf("using cached value for %s %d", name, channel)
			return token
		}
		log.Printf("couldn't cast value?")
	}
	log.Printf("not cached")
	key, err := attributevalue.Marshal(name)
	if err != nil {
		log.Printf("failure to get token for user %s", name)
		return ""
	}
	id, err := attributevalue.Marshal(channel)
	if err != nil {
		log.Printf("failure to get token for user %s", name)
		return ""
	}

	input := &dynamodb.GetItemInput{
		TableName: aws.String(ourConfig.TableName),
		Key: map[string]types.AttributeValue{
			"name":      key,
			"channelid": id,
		},
	}

	log.Printf("Calling Dynamodb with input: %v", input)
	result, err := tokens.GetItem(ctx, input)
	if err != nil {
		log.Printf("failure to get token for user %s %#v %s", name, input, err)
		return ""
	}

	if result.Item == nil {
		log.Printf("failure to get token for user %s %#v", name, result)
		return ""
	}

	t := new(TokenDB)
	err = attributevalue.UnmarshalMap(result.Item, t)
	if err != nil {
		return ""
	}
	tokenCache.Set(fmt.Sprintf("%s:%d", name, channel), t.Token)

	return t.Token
}
