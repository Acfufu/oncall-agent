// These are narrowly extracted source expressions/functions from
// Acfufu/oncall-agent @ 2e97742aae610b52535999a9851683320c349c6e.
// This is NOT the complete repository and does not test external integrations.
package repro

import (
    "crypto/md5"
    "crypto/sha1"
    "encoding/hex"
    "fmt"
    "math"
    "strings"
)

// Source: internal/queue/queue.go, taskID (unchanged function body).
func taskID(payload []byte) string {
    sum := sha1.Sum(payload)
    return hex.EncodeToString(sum[:16])
}

// Source: internal/agent/react.go, Run's no-citation guard.
// Wrapped in a function for isolated testing; control flow is unchanged.
func refusalGuard(final string, citationCount int) string {
    if citationCount == 0 && !strings.Contains(final, "未找到相关匹配") {
        if strings.TrimSpace(final) == "" {
            final = "未找到相关匹配：知识库中暂无与该问题相关的内容，未查询到相关告警/指标。请补充故障名或告警名后重试。"
        } else {
            final = "未找到相关匹配：知识库中暂无可引用的相关内容。\n\n" + final
        }
    }
    return final
}

// Source: internal/store/qdrant.go, cosine (unchanged function body).
func cosine(a, b []float32) float32 {
    n := len(a)
    if len(b) < n {
        n = len(b)
    }
    if n == 0 {
        return 0
    }
    var dot, na, nb float64
    for i := 0; i < n; i++ {
        dot += float64(a[i]) * float64(b[i])
        na += float64(a[i]) * float64(a[i])
        nb += float64(b[i]) * float64(b[i])
    }
    if na == 0 || nb == 0 {
        return 0
    }
    return float32(dot / (math.Sqrt(na) * math.Sqrt(nb)))
}

// Source: internal/rag/rag.go, AddDoc's point-ID expressions.
func chunkID(doc string, i int, snippet string) string {
    sum := md5.Sum([]byte(fmt.Sprintf("%s#%d#%s", doc, i, snippet)))
    return fmt.Sprintf("%x", sum)
}
