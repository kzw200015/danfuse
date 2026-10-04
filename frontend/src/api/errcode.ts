/** 成功 */
export const CODE_OK = 0

/** 通用失败：无需分支处理，直接提示 message。网络错误、响应格式错误等前端侧失败也使用该值 */
export const CODE_FAIL = 1

// 需要分支处理的业务码在这里定义，与后端 internal/pkg/errcode/codes.go 保持一致；目前没有
