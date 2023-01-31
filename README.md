# twitchAPILambda
Lambda function API for exercising the Twitch API via web requests

## Configuration

You must embed your configuration for the lambda to function.  Create a file config.json with a json containing

{
	"clientSecret": "TWITCH_CLIENT_SECRET",
	"clientID": "TWITCH_CLIENT_ID",
	"ourURL": "AWS_GATEWAY_URL",
	"tableName": "AWS_DYNAMODB_TABLE"
}

with the above values from your own setup
