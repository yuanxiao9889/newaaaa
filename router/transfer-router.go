package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-gonic/gin"
)

func setTransferRouter(router *gin.Engine) {
	router.POST("/internal/domestic/task-states", controller.DomesticReferenceTaskStates)
	router.POST("/internal/qiniu/authorize", controller.AuthorizeQiniuTransfer)
	// Machine credential + encrypted capability, not a user-supplied identity.
	router.POST("/internal/bridge/download-authorize", controller.AuthorizeBridgeDownload)
	group := router.Group("/v1/transfer", middleware.RouteTag("relay"), middleware.TransferControlAuth())
	group.POST("/qiniu-chunks/prepare", middleware.TokenAuth(), middleware.ReferenceUploadRateLimit(), controller.PrepareQiniuReferenceChunk)
	group.POST("/qiniu-chunks/complete", middleware.TokenAuth(), middleware.ReferenceUploadRateLimit(), controller.CompleteQiniuReferenceChunk)
	group.GET("/capabilities", controller.TransferCapabilities)
	group.GET("/requests/:key", controller.GetTransferRequest)
	group.POST("/download-tickets", controller.CreateTransferDownloadTicket)
	group.POST("/download-observations", middleware.TransferObservationRateLimit(), controller.AcceptTransferObservation)
}
