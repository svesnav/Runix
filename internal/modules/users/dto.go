package users

type createUserRequest struct {
	Username           string `json:"username" binding:"required"`
	Email              string `json:"email" binding:"required"`
	DisplayName        string `json:"displayName" binding:"max=128"`
	Password           string `json:"password" binding:"required"`
	MustChangePassword bool   `json:"mustChangePassword"`
}

type updateUserRequest struct {
	Email       string `json:"email" binding:"required"`
	DisplayName string `json:"displayName" binding:"max=128"`
	IsActive    *bool  `json:"isActive" binding:"required"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword" binding:"required"`
	NewPassword     string `json:"newPassword" binding:"required"`
}

type setPasswordRequest struct {
	NewPassword string `json:"newPassword" binding:"required"`
}
