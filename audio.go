package main

import (
 "context"
 "sync/atomic"
 "time"
 lksdk "github.com/livekit/server-sdk-go/v2"
 "github.com/pion/webrtc/v4/pkg/media"
)

type audioObservation struct {
 Samples atomic.Uint64
 Errors atomic.Uint64
 Binds atomic.Uint64
 LastAt atomic.Int64
}
type observedAudio struct {
 lksdk.SampleProvider
 observation *audioObservation
}
func (p *observedAudio) NextSample(ctx context.Context)(media.Sample,error){
 sample,err:=p.SampleProvider.NextSample(ctx)
 if err!=nil{p.observation.Errors.Add(1)}else{p.observation.Samples.Add(1);p.observation.LastAt.Store(time.Now().UnixMilli())}
 return sample,err
}
var audioObservations=[]*audioObservation{}
func audioSnapshots()[]map[string]any{
 result:=[]map[string]any{}
 for i,value:=range audioObservations{result=append(result,map[string]any{"publisher":i+1,"samples":value.Samples.Load(),"errors":value.Errors.Load(),"binds":value.Binds.Load(),"lastAt":value.LastAt.Load()})}
 return result
}
