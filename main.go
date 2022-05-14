package main

//go:generate mockgen -source=main.go -destination=mock_main/mock_gen.go

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"time"

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
	"github.com/aws/aws-sdk-go-v2/service/transcribe"
	"github.com/aws/aws-sdk-go-v2/service/transcribe/types"
)

const (
	BODY_LIMIT                      int = 10 * 1024 * 1024
	MAX_TRANSCRIPTION_JOB_WAIT_SECS int = 30
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

type ErrorResponse struct {
	Message     string   `json:"message"`
	AltMessages []string `json:"altMessages"`
}

func NewErrorResponse(message string, altMessages ...string) *ErrorResponse {
	if altMessages == nil {
		altMessages = make([]string, 0)
	}
	return &ErrorResponse{
		Message:     message,
		AltMessages: altMessages,
	}
}

type S3ObjectKeyResponse struct {
	Key string `json:"key"`
}

type TranscribeBody struct {
	Key                  string `json:"key"`
	MediaFormat          string `json:"mediaFormat"`
	MediaSampleRateHertz int32  `json:"mediaSampleRateHertz"`
}

type TranscribeResponse struct {
	Transcript string `json:"transcript"`
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

	mongoClient, err := mongo.Connect(context.Background(), options.Client().ApplyURI(
		os.Getenv("MONGO_URI"),
	))
	if err != nil {
		panic(err)
	}
	defer mongoClient.Disconnect(context.Background())
	visionClient, err := vision.NewImageAnnotatorClient(context.Background())
	if err != nil {
		panic(err)
	}
	defer visionClient.Close()
	awsCfg, err := config.LoadDefaultConfig(context.Background(), config.WithRegion("ap-southeast-1"))
	if err != nil {
		panic(err)
	}
	s3Client := s3.NewFromConfig(awsCfg)
	pollyClient := polly.NewFromConfig(awsCfg)
	transcribeClient := transcribe.NewFromConfig(awsCfg)
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
		_, err := coll.InsertOne(c.Context(), cardSet)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(NewErrorResponse(err.Error()))
		}
		return c.Status(fiber.StatusOK).JSON(cardSet)
	})
	cardSets.Get("/", func(c *fiber.Ctx) error {
		coll := mongoClient.Database("db").Collection("card-sets")
		cur, err := coll.Find(c.Context(), bson.D{})
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": err.Error(),
			})
		}
		result := []*CardSet{}
		for cur.Next(c.Context()) {
			cardSet := &CardSet{}
			err = cur.Decode(cardSet)
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(NewErrorResponse(err.Error()))
			}
			result = append(result, cardSet)
		}
		return c.Status(fiber.StatusOK).JSON(result)
	})
	cardSets.Get("/:id", func(c *fiber.Ctx) error {
		objId, err := primitive.ObjectIDFromHex(c.Params("id"))
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(NewErrorResponse(err.Error()))
		}
		coll := mongoClient.Database("db").Collection("card-sets")
		result := coll.FindOne(c.Context(), bson.M{"_id": bson.M{"$eq": objId}})
		cardSet := &CardSet{}
		err = result.Decode(cardSet)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(NewErrorResponse(err.Error(), "the card set was not found"))
		}
		return c.Status(fiber.StatusOK).JSON(cardSet)
	})
	cardSets.Put("/:id", func(c *fiber.Ctx) error {
		cardSet := &CardSet{}
		c.BodyParser(cardSet)
		id := c.Params("id")
		objId, err := primitive.ObjectIDFromHex(id)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(NewErrorResponse(err.Error()))
		}
		cardSet.Id = objId
		for _, card := range cardSet.Cards {
			card.Id = primitive.NewObjectID()
		}
		coll := mongoClient.Database("db").Collection("card-sets")
		result, err := coll.ReplaceOne(c.Context(), bson.M{"_id": bson.M{"$eq": objId}}, cardSet)
		if result.MatchedCount == 0 {
			return c.Status(fiber.StatusInternalServerError).JSON(NewErrorResponse("the card set was not found"))
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(NewErrorResponse(err.Error()))
		}
		return c.Status(fiber.StatusOK).JSON(cardSet)
	})
	cardSets.Delete("/:id", func(c *fiber.Ctx) error {
		id := c.Params("id")
		objId, err := primitive.ObjectIDFromHex(id)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(NewErrorResponse(err.Error()))
		}
		coll := mongoClient.Database("db").Collection("card-sets")
		result, err := coll.DeleteOne(c.Context(), bson.M{"_id": bson.M{"$eq": objId}})
		if result.DeletedCount == 0 {
			return c.Status(fiber.StatusInternalServerError).JSON(NewErrorResponse("the card set was not found"))
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(NewErrorResponse(err.Error()))
		}
		return c.SendStatus(fiber.StatusOK)
	})
	app.Post("/label", func(c *fiber.Ctx) error {
		fileHeader, err := c.FormFile("image")
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(NewErrorResponse(err.Error()))
		}
		file, err := fileHeader.Open()
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(NewErrorResponse(err.Error()))
		}
		defer file.Close()
		image, err := vision.NewImageFromReader(file)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(NewErrorResponse(err.Error()))
		}
		labels, err := visionClient.DetectLabels(c.Context(), image, nil, 10)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(NewErrorResponse(err.Error()))
		}
		return c.Status(fiber.StatusOK).JSON(labels)
	})
	app.Post("/upload", func(c *fiber.Ctx) error {
		fileHeader, err := c.FormFile("file")
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(NewErrorResponse(err.Error()))
		}
		file, err := fileHeader.Open()
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(NewErrorResponse(err.Error()))
		}
		key, err := randomHex(16)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(NewErrorResponse(err.Error()))
		}
		_, err = s3Client.PutObject(c.Context(), &s3.PutObjectInput{
			Body:   file,
			Bucket: aws.String(os.Getenv("BUCKET_NAME")),
			Key:    aws.String(key),
		})
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(NewErrorResponse(err.Error()))
		}
		return c.Status(fiber.StatusOK).JSON(&S3ObjectKeyResponse{
			Key: key,
		})
	})
	app.Post("/read", func(c *fiber.Ctx) error {
		body := &ReadBody{}
		err := c.BodyParser(body)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(NewErrorResponse(err.Error()))
		}
		response, err := pollyClient.SynthesizeSpeech(c.Context(), &polly.SynthesizeSpeechInput{
			Text: &body.Text, OutputFormat: "mp3", VoiceId: "Joanna",
		})
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(NewErrorResponse(err.Error()))
		}
		buf := make([]byte, 64)
		c.Set("Content-Type", "audio/mp3")
		for {
			n, err := response.AudioStream.Read(buf)
			if err != nil {
				break
			}
			c.Write(buf[:n])
		}
		return c.SendStatus(fiber.StatusOK)
	})
	app.Post("/transcribe", func(c *fiber.Ctx) error {
		body := &TranscribeBody{}
		err := c.BodyParser(body)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(NewErrorResponse(err.Error()))
		}
		transcribeClient.StartTranscriptionJob(c.Context(), &transcribe.StartTranscriptionJobInput{
			TranscriptionJobName: &body.Key,
			LanguageCode:         types.LanguageCodeEnUs,
			MediaSampleRateHertz: aws.Int32(body.MediaSampleRateHertz),
			MediaFormat:          types.MediaFormat(body.MediaFormat),
			Media: &types.Media{
				MediaFileUri: aws.String(fmt.Sprintf("s3://%s/%s", os.Getenv("BUCKET_NAME"), body.Key)),
			},
		})
		for i := 0; i < MAX_TRANSCRIPTION_JOB_WAIT_SECS; i++ {
			job, err := transcribeClient.GetTranscriptionJob(c.Context(), &transcribe.GetTranscriptionJobInput{
				TranscriptionJobName: &body.Key,
			})
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(NewErrorResponse(err.Error()))
			}
			if job.TranscriptionJob.TranscriptionJobStatus == types.TranscriptionJobStatusCompleted {
				uri := *job.TranscriptionJob.Transcript.TranscriptFileUri
				resp, err := http.Get(uri)
				if err != nil {
					return c.Status(fiber.StatusInternalServerError).JSON(NewErrorResponse(err.Error()))
				}
				defer resp.Body.Close()
				body, err := ioutil.ReadAll(resp.Body)
				if err != nil {
					return c.Status(fiber.StatusInternalServerError).JSON(NewErrorResponse(err.Error()))
				}
				j := make(map[string]any)
				err = json.Unmarshal(body, &j)
				if err != nil {
					return c.Status(fiber.StatusInternalServerError).JSON(NewErrorResponse(err.Error()))
				}
				c.Status(fiber.StatusOK).JSON(j)
				break
			}
			time.Sleep(1 * time.Second)
		}
		return nil
	})
	app.Listen(":3000")
}
