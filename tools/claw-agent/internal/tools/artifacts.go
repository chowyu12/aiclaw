package tools

// 工具产出的文件：生成的图、合成的语音。
//
// 与 Env.Attachments 是两回事：那些图是**给模型看的**（进模型历史），这里的
// 路径是**给用户看的**（界面把图画出来、把音频放出来）。一次生成两者都要——
// 模型要判断这张图对不对，用户要能打开它。
//
// 为什么按 context 传而不是挂在 Env 上：Env 是每轮一个，而一轮里的工具调用
// 可能并发跑，挂在 Env 上会让两个调用的产出物串在一起，界面上就是「A 步骤
// 下面挂着 B 步骤生成的图」。

import (
	"context"
	"sync"
)

type artifactKey struct{}

// ArtifactSink 收集一次工具调用产出的文件路径（相对工作区）。
type ArtifactSink struct {
	mu    sync.Mutex
	paths []string
}

// Add 记下一个产出物。
func (s *ArtifactSink) Add(path string) {
	if s == nil || path == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paths = append(s.paths, path)
}

// Paths 取走已记录的路径。
func (s *ArtifactSink) Paths() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...)
}

// WithArtifacts 把收集器放进 context，一次工具调用一个。
func WithArtifacts(ctx context.Context, sink *ArtifactSink) context.Context {
	return context.WithValue(ctx, artifactKey{}, sink)
}

// Produce 记一个产出物。不在工具调用里（没有收集器）时什么都不做——
// 产出物只是给界面的额外信息，缺了不该让工具失败。
func Produce(ctx context.Context, path string) {
	if sink, ok := ctx.Value(artifactKey{}).(*ArtifactSink); ok {
		sink.Add(path)
	}
}
