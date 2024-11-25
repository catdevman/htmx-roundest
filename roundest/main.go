package main

import (
	"cmp"
	"context"
	"embed"
	"encoding/base64"
	"fmt"
	htmltmpl "html/template"
	"log"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	fiberadapter "github.com/awslabs/aws-lambda-go-api-proxy/fiber"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/template/html/v2"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go/aws"
)

var app *fiber.App
var fiberLambda *fiberadapter.FiberLambda

type Pokemon struct {
	PK           string `dynamodbav:"pk" json:"-"`
	SK           string `dynamodbav:"sk" json:"-"`
	ID           int64  `dynamodbav:"id" json:"id"`
	Name         string `dynamodbav:"name" json:"name"`
	Image        string `dynamodbav:"image" json:"image"`
	ImageBlob    []byte `dynamodbav:"image_blob" json:"-"`
	EncodedImage string
	WinCount     int64     `dynamodbav:"win_count" json:"winCount"`
	LossCount    int64     `dynamodbav:"loss_count" json:"lossCount"`
	InsertedAt   time.Time `dynamodbav:"inserted_at" json:"insertedAt"`
	UpdatedAt    time.Time `dynamodbav:"updated_at" json:"updatedAt"`
}

func (p Pokemon) CalculateWinRatio() float64 {
	total := (p.WinCount + p.LossCount)
	if total == 0 {
		return 0
	}
	return float64(p.WinCount / (total))
}

var defaultDDBOptions = func(o *dynamodb.Options) {
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") == "" {
		o.BaseEndpoint = aws.String("http://127.0.0.1:8000")
	}
}

//go:embed static
var StaticContent embed.FS

func init() {
	// This is all in the init for the benefit of aws lambd
	engine := html.New("./views", ".html")

	app = fiber.New(fiber.Config{
		Views: engine,
	})

	app.Get("/", func(c *fiber.Ctx) error {
		pokemons := getTwoRandomPokemon()
		for i := range pokemons {
			pokemons[i].EncodedImage = base64.StdEncoding.EncodeToString(pokemons[i].ImageBlob)
		}
		log.Println(pokemons[0].EncodedImage == pokemons[1].EncodedImage)
		return c.Render("index", fiber.Map{
			"Title":    "Rounder",
			"Pokemon1": pokemons[0],
			"Pokemon2": pokemons[1],
		}, "layouts/main")
	})

	app.Post("/vote/:info", func(c *fiber.Ctx) error {
		pokemon := strings.Split(c.Params("info"), ",")
		log.Println("winner", pokemon[0])
		log.Println("loser", pokemon[1])
		addWin(pokemon[0])
		addLoss(pokemon[1])
		pokemons := getTwoRandomPokemon()
		tmpl, _ := htmltmpl.ParseGlob("./views/*.html")
		for i := range pokemons {
			pokemons[i].EncodedImage = base64.StdEncoding.EncodeToString(pokemons[i].ImageBlob)
		}
		log.Println(pokemons[0].EncodedImage == pokemons[1].EncodedImage)
		data := struct {
			Title    string
			Pokemon1 Pokemon
			Pokemon2 Pokemon
		}{
			"Rounder",
			pokemons[0],
			pokemons[1],
		}
		return tmpl.ExecuteTemplate(c, "pokey-votes", data)
	})

	app.Get("/results", func(c *fiber.Ctx) error {
		pokemons := []Pokemon{}
		cfg, err := config.LoadDefaultConfig(context.TODO())
		if err != nil {
			log.Println(err)
			return c.SendString("sucks to suck; could not setup aws configuration")
		}
		svc := dynamodb.NewFromConfig(cfg, defaultDDBOptions)
		keyCond := expression.KeyAnd(
			expression.Key("pk").Equal(expression.Value("pokemon")),
			expression.Key("sk").BeginsWith("id#"),
		)
		expr, err := expression.NewBuilder().WithKeyCondition(keyCond).Build()
		response, err := svc.Query(context.TODO(), &dynamodb.QueryInput{
			TableName:                 aws.String(os.Getenv("DDB_TABLE")),
			KeyConditionExpression:    expr.KeyCondition(),
			ExpressionAttributeNames:  expr.Names(),
			ExpressionAttributeValues: expr.Values(),
		})
		if err != nil {
			log.Printf("Here's why: %v\n", err)
		}

		err = attributevalue.UnmarshalListOfMaps(response.Items, &pokemons)
		if err != nil {
			log.Printf("Couldn't unmarshal query response. Here's why: %v\n", err)
		}
		for i := range pokemons {
			pokemons[i].EncodedImage = base64.StdEncoding.EncodeToString(pokemons[i].ImageBlob)
		}
		slices.SortFunc(pokemons, func(a, b Pokemon) int {
			return cmp.Or(
				-cmp.Compare(a.WinCount, b.WinCount),
				-cmp.Compare(a.CalculateWinRatio(), b.CalculateWinRatio()),
			)
		})
		return c.Render("results", fiber.Map{
			"Title":    "Rounder",
			"Pokemons": pokemons,
		}, "layouts/main")
	})

	fiberLambda = fiberadapter.New(app)
}

// Handler will deal with Fiber working with Lambda
func Handler(req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	return fiberLambda.Proxy(req)
}

func main() {
	// Make the handler available for Remote Procedure Call by AWS Lambda
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		lambda.Start(Handler)
	} else {
		log.Fatal(app.Listen(":8989"))
	}
}

func getTwoRandomPokemon() []Pokemon {
	pokemon := []Pokemon{}
	poke := Pokemon{}
	poke1Id := intInRange(1, 1000)
	poke2Id := intInRange(1, 1000)
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		log.Println(err)
		return pokemon
	}
	svc := dynamodb.NewFromConfig(cfg, defaultDDBOptions)
	pk, err := attributevalue.Marshal("pokemon")
	sk1, err := attributevalue.Marshal(fmt.Sprintf("id#%d", poke1Id))
	sk2, err := attributevalue.Marshal(fmt.Sprintf("id#%d", poke2Id))
	response1, err := svc.GetItem(context.TODO(), &dynamodb.GetItemInput{
		TableName: aws.String(os.Getenv("DDB_TABLE")),
		Key: map[string]types.AttributeValue{
			"pk": pk,
			"sk": sk1,
		},
	})
	if err != nil {
		log.Printf("Here's why: %v\n", err)
	}

	response2, err := svc.GetItem(context.TODO(), &dynamodb.GetItemInput{
		TableName: aws.String(os.Getenv("DDB_TABLE")),
		Key: map[string]types.AttributeValue{
			"pk": pk,
			"sk": sk2,
		},
	})
	err = attributevalue.UnmarshalMap(response1.Item, &poke)
	pokemon = append(pokemon, poke)
	err = attributevalue.UnmarshalMap(response2.Item, &poke)
	pokemon = append(pokemon, poke)

	return pokemon
}

func addWin(id string) {
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		log.Println(err)
		return
	}
	pk, err := attributevalue.Marshal("pokemon")
	sk, err := attributevalue.Marshal(fmt.Sprintf("id#%s", id))

	svc := dynamodb.NewFromConfig(cfg, defaultDDBOptions)
	svc.UpdateItem(context.TODO(), &dynamodb.UpdateItemInput{
		TableName: aws.String(os.Getenv("DDB_TABLE")),
		Key: map[string]types.AttributeValue{
			"pk": pk,
			"sk": sk,
		},
		UpdateExpression: aws.String("set win_count = win_count + :value"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":value": &types.AttributeValueMemberN{Value: "1"},
		},
	})

}

func addLoss(id string) {
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		log.Println(err)
		return
	}
	pk, err := attributevalue.Marshal("pokemon")
	sk, err := attributevalue.Marshal(fmt.Sprintf("id#%s", id))

	svc := dynamodb.NewFromConfig(cfg, defaultDDBOptions)
	svc.UpdateItem(context.TODO(), &dynamodb.UpdateItemInput{
		TableName: aws.String(os.Getenv("DDB_TABLE")),
		Key: map[string]types.AttributeValue{
			"pk": pk,
			"sk": sk,
		},
		UpdateExpression: aws.String("set loss_count = loss_count + :value"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":value": &types.AttributeValueMemberN{Value: "1"},
		},
	})

}

func intInRange(min, max int) int {
	return rand.IntN(max-min) + min
}
