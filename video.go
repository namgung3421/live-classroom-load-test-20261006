package main

import (
 "bytes"
 "context"
 "fmt"
 "io"
 "os"
 "sync"
 "time"

 "github.com/livekit/protocol/livekit"
 lksdk "github.com/livekit/server-sdk-go/v2"
 "github.com/pion/webrtc/v4"
 "github.com/pion/webrtc/v4/pkg/media"
 "github.com/pion/webrtc/v4/pkg/media/ivfreader"
)

var layerData=map[string][]byte{}
var layerLock sync.Mutex

type fixtureLooper struct {
 lksdk.BaseSampleProvider
 data []byte
 reader *ivfreader.IVFReader
}

func (p *fixtureLooper) NextSample(_ context.Context)(media.Sample,error){
 for attempts:=0;attempts<2;attempts++{
  if p.reader==nil{
   reader,_,err:=ivfreader.NewWith(bytes.NewReader(p.data));if err!=nil{return media.Sample{},err};p.reader=reader
  }
  frame,_,err:=p.reader.ParseNextFrame()
  if err==io.EOF{p.reader=nil;continue}
  if err!=nil{return media.Sample{},err}
  return media.Sample{Data:frame,Duration:50*time.Millisecond},nil
 }
 return media.Sample{},fmt.Errorf("generated fixture has no frames")
}

func generatedVideoTracks()([]*lksdk.LocalTrack,error){
 tracks:=[]*lksdk.LocalTrack{}
 specs:=[]struct{name string;width,height,bitrate uint32;quality livekit.VideoQuality}{
  {"low",320,180,150000,livekit.VideoQuality_LOW},
  {"medium",640,360,600000,livekit.VideoQuality_MEDIUM},
  {"high",1920,1080,3000000,livekit.VideoQuality_HIGH},
 }
 for _,spec:=range specs{
  layerLock.Lock()
  data,ok:=layerData[spec.name]
  if !ok{
   var err error;data,err=os.ReadFile("video/"+spec.name+".ivf")
   if err!=nil{layerLock.Unlock();return nil,fmt.Errorf("missing generated video layer")}
   _,header,err:=ivfreader.NewWith(bytes.NewReader(data))
   if err!=nil||uint32(header.Width)!=spec.width||uint32(header.Height)!=spec.height{layerLock.Unlock();return nil,fmt.Errorf("invalid generated video dimensions")}
   layerData[spec.name]=data
  }
  layerLock.Unlock()
  codec:=webrtc.RTPCodecCapability{MimeType:webrtc.MimeTypeVP8,ClockRate:90000,RTCPFeedback:[]webrtc.RTCPFeedback{{Type:"nack"},{Type:"nack",Parameter:"pli"}}}
  layer:=&livekit.VideoLayer{Quality:spec.quality,Width:spec.width,Height:spec.height,Bitrate:spec.bitrate}
  track,err:=lksdk.NewLocalTrack(codec,lksdk.WithSimulcast("synthetic-camera",layer))
  if err==nil{err=track.StartWrite(&fixtureLooper{data:data},nil)}
  if err!=nil{return nil,fmt.Errorf("generated video track setup failed (%T)",err)}
  tracks=append(tracks,track)
 }
 return tracks,nil
}
