package main

import (
 "context"
 "encoding/base64"
 "encoding/json"
 "fmt"
 "os"
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
var audioPackets=[][]byte{}
type generatedAudioProvider struct{lksdk.BaseSampleProvider;index int}
func generatedAudio()(*generatedAudioProvider,error){
 if len(audioPackets)==0{
  data,err:=os.ReadFile("video/audio.json");if err!=nil{return nil,err}
  var encoded []string;if json.Unmarshal(data,&encoded)!=nil||len(encoded)<250{return nil,fmt.Errorf("invalid generated audio")}
  for _,value:=range encoded{packet,err:=base64.StdEncoding.DecodeString(value);if err!=nil||len(packet)==0{return nil,fmt.Errorf("invalid generated Opus packet")};audioPackets=append(audioPackets,packet)}
 }
 return &generatedAudioProvider{},nil
}
func (p *generatedAudioProvider) NextSample(_ context.Context)(media.Sample,error){
 packet:=audioPackets[p.index];p.index=(p.index+1)%len(audioPackets)
 return media.Sample{Data:packet,Duration:20*time.Millisecond},nil
}
func audioSnapshots()[]map[string]any{
 result:=[]map[string]any{}
 for i,value:=range audioObservations{result=append(result,map[string]any{"publisher":i+1,"samples":value.Samples.Load(),"errors":value.Errors.Load(),"binds":value.Binds.Load(),"lastAt":value.LastAt.Load()})}
 return result
}
