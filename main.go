package main

//go:generate mockgen -source=main.go -destination=mock_main/mock_gen.go

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"

	vision "cloud.google.com/go/vision/apiv1"
	"github.com/gofiber/fiber/v2"
	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/polly"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	BODY_LIMIT int = 10 * 1024 * 1024
)

type ReadBody struct {
	Text string `json:"text"`
}

type CardSet struct {
	Id         primitive.ObjectID `json:"id" bson:"_id"`
	Name       string             `json:"name" bson:"name"`
	CoverImage string             `json:"coverImage" bson:"coverImage"`
	Cards      []*Card            `json:"cards" bson:"cards"`
}

type Card struct {
	Id    primitive.ObjectID `json:"id" bson:"_id"`
	Word  string             `json:"word" bson:"word"`
	Image string             `json:"image" bson:"image"`
}

func randomHex(n int) (string, error) {
	bytes := make([]byte, n)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func main() {
	godotenv.Load()

	mongoClient, _ := mongo.Connect(context.Background(), options.Client().ApplyURI(
		os.Getenv("MONGO_URI"),
	))
	defer mongoClient.Disconnect(context.Background())
	visionClient, _ := vision.NewImageAnnotatorClient(context.Background())
	defer visionClient.Close()
	awsCfg, _ := config.LoadDefaultConfig(context.Background(), config.WithRegion("ap-southeast-1"))
	s3Client := s3.NewFromConfig(awsCfg)
	pollyClient := polly.NewFromConfig(awsCfg)

	app := fiber.New(fiber.Config{
		BodyLimit: BODY_LIMIT,
	})
	cardSets := app.Group("/card-sets")
	cardSets.Post("/", func(c *fiber.Ctx) error {
		cardSet := &CardSet{}
		c.BodyParser(cardSet)
		cardSet.Id = primitive.NewObjectID()
		for _, card := range cardSet.Cards {
			card.Id = primitive.NewObjectID()
		}
		coll := mongoClient.Database("db").Collection("card-sets")
		coll.InsertOne(c.Context(), cardSet)
		return nil
	})
	cardSets.Get("/", func(c *fiber.Ctx) error {
		coll := mongoClient.Database("db").Collection("card-sets")
		cur, _ := coll.Find(c.Context(), bson.D{})
		result := []*CardSet{}
		for cur.Next(c.Context()) {
			cardSet := &CardSet{}
			cur.Decode(cardSet)
			result = append(result, cardSet)
		}
		return c.JSON(result)
	})
	cardSets.Get("/:id", func(c *fiber.Ctx) error {
		coll := mongoClient.Database("db").Collection("card-sets")
		objId, _ := primitive.ObjectIDFromHex(c.Params("id"))
		result := coll.FindOne(c.Context(), bson.M{"_id": bson.M{"$eq": objId}})
		cardSet := &CardSet{}
		result.Decode(cardSet)
		return c.JSON(cardSet)
	})
	cardSets.Put("/:id", func(c *fiber.Ctx) error {
		cardSet := &CardSet{}
		c.BodyParser(cardSet)
		id := c.Params("id")
		objId, _ := primitive.ObjectIDFromHex(id)
		cardSet.Id = objId
		for _, card := range cardSet.Cards {
			card.Id = primitive.NewObjectID()
		}
		coll := mongoClient.Database("db").Collection("card-sets")
		coll.ReplaceOne(c.Context(), bson.M{"_id": bson.M{"$eq": objId}}, cardSet)
		return nil
	})
	cardSets.Delete("/:id", func(c *fiber.Ctx) error {
		cardSet := &CardSet{}
		c.BodyParser(cardSet)
		id := c.Params("id")
		objId, _ := primitive.ObjectIDFromHex(id)
		coll := mongoClient.Database("db").Collection("card-sets")
		coll.DeleteOne(c.Context(), bson.M{"_id": bson.M{"$eq": objId}})
		return nil
	})
	app.Post("/label", func(c *fiber.Ctx) error {
		fileHeader, _ := c.FormFile("image")
		file, _ := fileHeader.Open()
		defer file.Close()
		image, _ := vision.NewImageFromReader(file)
		labels, _ := visionClient.DetectLabels(c.Context(), image, nil, 10)
		c.Status(fiber.StatusOK).JSON(labels)
		return nil
	})
	app.Post("/upload", func(c *fiber.Ctx) error {
		fileHeader, _ := c.FormFile("file")
		file, _ := fileHeader.Open()
		key, _ := randomHex(16)
		s3Client.PutObject(c.Context(), &s3.PutObjectInput{
			Body:   file,
			Bucket: aws.String(os.Getenv("BUCKET_NAME")),
			Key:    aws.String(key),
		})
		return c.SendString(key)
	})
	app.Post("/read", func(c *fiber.Ctx) error {
		body := &ReadBody{}
		c.BodyParser(body)
		response, _ := pollyClient.SynthesizeSpeech(c.Context(), &polly.SynthesizeSpeechInput{
			Text: &body.Text, OutputFormat: "mp3", VoiceId: "Joanna",
		})
		buf := make([]byte, 64)
		c.Set("Content-Type", "audio/mp3")
		for {
			n, err := response.AudioStream.Read(buf)
			if err != nil {
				break
			}
			c.Write(buf[:n])
		}
		return nil
	})
	app.Post("/speak", func(c *fiber.Ctx) error {
		return nil
	})
	app.Listen(":3000")
}
