package alternate

import (
 "errors"
 "strings"
 "testing"
)

func TestDecodeAntigravitySSEStopAndThoughtFiltering(t *testing.T) {
 input:=strings.Join([]string{
  "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"secret reasoning\",\"thought\":true},{\"text\":\"Hello\"}]}}]}}",
  "",
  "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\" world\"}]},\"finishReason\":\"STOP\"}]}}",
  "",
 }, "\n")
 got,err:=decodeAntigravitySSE(strings.NewReader(input))
 if err!=nil{t.Fatal(err)}
 if got.Text!="Hello world"||got.FinishReason!="STOP"{t.Fatalf("%+v",got)}
}
func TestDecodeAntigravitySSEContinuation(t *testing.T){
 input:=`data: {"response":{"candidates":[{"content":{"parts":[{"text":"first"}]},"finishReason":"CONTINUATION","continuationToken":"cursor-1"}]}}`
 got,err:=decodeAntigravitySSE(strings.NewReader(input))
 if err!=nil{t.Fatal(err)}
 if got.ContinuationToken!="cursor-1"||got.FinishReason!="CONTINUATION" {t.Fatalf("%+v",got)}
}
func TestDecodeAntigravitySSERejectsMissingFinish(t *testing.T){
 _,err:=decodeAntigravitySSE(strings.NewReader(`data: {"response":{"candidates":[{"content":{"parts":[{"text":"partial"}]}}]}}`))
 var api *Error
 if !errors.As(err,&api)||api.Code!="incomplete_stream"{t.Fatalf("unexpected error: %v",err)}
}
func TestDecodeAntigravitySSEBlocksRejectedPrompt(t *testing.T){
 _,err:=decodeAntigravitySSE(strings.NewReader(`data: {"response":{"promptFeedback":{"blockReason":"SAFETY"}}}`))
 var api *Error
 if !errors.As(err,&api)||api.Code!="blocked_prompt"{t.Fatalf("unexpected error: %v",err)}
}
func TestGeminiWebChannelFinishAndMultiplePayloads(t *testing.T){
 completed:=[]any{[]any{nil,nil,nil,[]any{float64(1)}}}
 if !webFrameEnded(completed){t.Fatal("channel completion frame not recognized")}
 candidate:=make([]any,9);candidate[1]=[]any{"hello"};candidate[8]=[]any{float64(2)}
 payload:=make([]any,5);payload[4]=[]any{candidate}
 element:=make([]any,4);element[3]=[]any{nil,nil,nil,payload}
 message:=make([]any,7);message[6]=[]any{element,element}
 frame:=[]any{[]any{nil,message}}
 roots:=webFramePayloads(frame)
 if len(roots)!=2{t.Fatalf("payload count=%d",len(roots))}
 if text,done:=webExtractText(roots[0]);text!="hello"||!done{t.Fatalf("text=%q done=%v",text,done)}
}
