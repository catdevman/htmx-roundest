package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const (
	graphqlURL = "https://beta.pokeapi.co/graphql/v1beta"
	query      = `query {
        pokemon_v2_pokemon(order_by: {id: asc}) {
          name
          id
          pokemon_v2_pokemonspecy {
            name
          }
        }
      }`
)

type Pokemon struct {
	PK         string    `dynamodbav:"pk" json:"-"`
	SK         string    `dynamodbav:"sk" json:"-"`
	ID         int       `dynamodbav:"id" json:"id"`
	Name       string    `dynamodbav:"name" json:"name"`
	Image      string    `dynamodbav:"image" json:"image"`
	ImageBlob  []byte    `dynamodbav:"image_blob" json:"-"`
	WinCount   int       `dynamodbav:"win_count" json:"winCount"`
	LossCount  int       `dynamodbav:"loss_count" json:"lossCount"`
	InsertedAt time.Time `dynamodbav:"inserted_at" json:"insertedAt"`
	UpdatedAt  time.Time `dynamodbav:"updated_at" json:"updatedAt"`
}

type GraphQLResponse struct {
	Data struct {
		Pokemon []struct {
			ID           int `json:"id"`
			PokemonSpecy struct {
				Name string `json:"name"`
			} `json:"pokemon_v2_pokemonspecy"`
		} `json:"pokemon_v2_pokemon"`
	} `json:"data"`
}

type PokemonData struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func fetchAllPokemon() ([]PokemonData, error) {
	reqBody := map[string]string{
		"query": query,
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("error marshaling query: %w", err)
	}

	req, err := http.NewRequest("POST", graphqlURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error making request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed with status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %w", err)
	}

	var graphQLResp GraphQLResponse
	if err := json.Unmarshal(body, &graphQLResp); err != nil {
		return nil, fmt.Errorf("error parsing JSON response: %w", err)
	}

	pokemonList := make([]PokemonData, len(graphQLResp.Data.Pokemon))
	for i, p := range graphQLResp.Data.Pokemon {
		pokemonList[i] = PokemonData{
			ID:   p.ID,
			Name: p.PokemonSpecy.Name,
		}
	}

	return pokemonList, nil
}

func main() {
	ctx := context.Background()
	sdkConfig, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"))
	if err != nil {
		errors.Join(err, errors.New("error creating dynamodb session"))
	}
	svc := dynamodb.NewFromConfig(sdkConfig, func(o *dynamodb.Options) {
		// o.BaseEndpoint = aws.String("http://127.0.0.1:8000")
	})
	pokemons, _ := fetchAllPokemon()
	for _, pokemon := range pokemons {
		id := strconv.Itoa(pokemon.ID)
		file, _ := os.Open("../../rounder/static/images/" + id + ".png")
		all, _ := io.ReadAll(file)
		poke := Pokemon{
			PK:         "pokemon",
			SK:         "id#" + id,
			ID:         int(pokemon.ID),
			Name:       pokemon.Name,
			Image:      "static/images/" + id + ".png",
			ImageBlob:  all,
			UpdatedAt:  time.Now(),
			InsertedAt: time.Now(),
			WinCount:   0,
			LossCount:  0,
		}
		item, err := attributevalue.MarshalMap(poke)
		if err != nil {
			panic(err)
		}
		_, err = svc.PutItem(ctx, &dynamodb.PutItemInput{
			TableName:    aws.String(os.Getenv("DDB_TABLE")),
			Item:         item,
			ReturnValues: types.ReturnValueAllOld,
		})
		if err != nil {
			panic(err)
		}
	}
}
