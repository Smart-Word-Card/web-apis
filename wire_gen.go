// Code generated by Wire. DO NOT EDIT.

//go:generate go run github.com/google/wire/cmd/wire
//go:build !wireinject
// +build !wireinject

package main

// Injectors from wire.go:

func InitializeFiberApp() *FiberApp {
	manageService := NewManageService()
	manageRoutes := NewManageRoutes(manageService)
	fiberApp := NewFiberApp(manageRoutes)
	return fiberApp
}
