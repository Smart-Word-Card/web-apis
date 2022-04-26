package main

//go:generate mockgen -source=main.go -destination=mock_main/mock_gen.go

import (
	"fmt"

	vision "cloud.google.com/go/vision/apiv1"
	"github.com/gofiber/fiber/v2"
)

type FiberApp struct {
	app *fiber.App
}

func NewFiberApp(manageRoutes IManageRoutes) *FiberApp {
	fiberApp := &FiberApp{app: fiber.New(fiber.Config{
		BodyLimit: 10 * 1024 * 1024,
	})}

	manageRoutes.ProvideRoutes(fiberApp)

	return fiberApp
}

type IManageRoutes interface {
	ProvideRoutes(fiberApp *FiberApp)
	LabelImage(ctx *fiber.Ctx) error
	// UploadImage(ctx *fiber.Ctx) error
	CreateCollection(ctx *fiber.Ctx) error
	GetCollection(ctx *fiber.Ctx) error
}

type ManageRoutes struct {
	ManageService IManageService
}

type LabelImageBody struct {
	Image interface{} `form:"image"`
}

func (r *ManageRoutes) LabelImage(ctx *fiber.Ctx) error {
	client, _ := vision.NewImageAnnotatorClient(ctx.Context())
	defer client.Close()
	fileHeader, _ := ctx.FormFile("image")
	file, _ := fileHeader.Open()
	defer file.Close()
	image, _ := vision.NewImageFromReader(file)
	labels, _ := client.DetectLabels(ctx.Context(), image, nil, 10)
	ctx.Status(fiber.StatusOK).JSON(labels)
	// if form, err := ctx.MultipartForm(); err == nil {
	// 	files := form.File["documents"]
	// 	for _, file := range files {
	// 		fmt.Println(file.Filename, file.Size, file.Header["Content-Type"][0])
	// 		f, _ := file.Open()
	// 		img, _ := vision.NewImageFromReader(f)
	// 		labels, _ := client.DetectLabels(ctx.Context(), img, nil, 10)
	// 		fmt.Println("Labels:")
	// 		for _, label := range labels {
	// 			fmt.Println(label.Description)
	// 		}
	// 		f.Close()
	// 	}
	// }
	return nil
}

// func (r *ManageRoutes) UploadImage(ctx *fiber.Ctx) error {
// sess, _ := session.NewSession(&aws.Config{
// 	Credentials: &credentials.Credentials{},
// })
// }

func (r *ManageRoutes) CreateCollection(ctx *fiber.Ctx) error {
	r.ManageService.CreateCollection(ctx)
	return nil
}

func (r *ManageRoutes) GetCollection(ctx *fiber.Ctx) error {
	r.ManageService.CreateCollection(ctx)
	return nil
}

func NewManageRoutes(manageService IManageService) *ManageRoutes {
	return &ManageRoutes{ManageService: manageService}
}

func (r *ManageRoutes) ProvideRoutes(f *FiberApp) {
	manage := f.app.Group("/manage")

	manage.Post("/label", r.LabelImage)
	// manage.Post("/upload", r.UploadImage)
	manage.Post("/collections", r.CreateCollection)
	manage.Get("/collection", r.GetCollection)
}

type IManageService interface {
	CreateCollection(ctx *fiber.Ctx)
}

type ManageService struct{}

func (s *ManageService) CreateCollection(ctx *fiber.Ctx) {
	fmt.Println("create collection")
}

func NewManageService() *ManageService {
	return &ManageService{}
}

func main() {
	fiberApp := InitializeFiberApp()
	fiberApp.app.Listen(":3000")
}
