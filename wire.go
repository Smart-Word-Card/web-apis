//go:build wireinject
// +build wireinject

package main

import "github.com/google/wire"

func InitializeFiberApp() *FiberApp {
	wire.Build(
		NewFiberApp,
		wire.Bind(new(IManageRoutes), new(*ManageRoutes)),
		NewManageRoutes,
		wire.Bind(new(IManageService), new(*ManageService)),
		NewManageService,
	)
	return &FiberApp{}
}
