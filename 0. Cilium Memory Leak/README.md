# Cilium 메모리 누수 문제 모방을 통한 공부 - KAKAO

## 서론
카카오 클라우드 플랫폼에서 사용한 방법을 참고하여 어떤 방식으로 그들이 문제에 대해서 접근하고 이 문제를 해결 했는지 알아보기 위해서 공부한 레포지토리 입니다. 이 점 참고해 주시기 바랍니다.

## 소개
이 리포지토리는 Cilium에서 발생할 수 있는 메모리 누수 문제를 해결하는 방법을 설명합니다. Cilium은 쿠버네티스 환경에서 네트워크 정책과 보안을 관리하는 데 사용되는 오픈소스 네트워크 플러그인입니다. 그러나 특정 상황에서 메모리 누수가 발생할 수 있으며, 이를 적절히 처리하지 않으면 시스템 자원을 소모하여 성능 문제를 일으킬 수 있습니다.

## What is Cilium?

- 쿠버네티스의 네트워크 모델의 요구사항을 구현한 것이 CNI Plugin
- 카카오 클라우드는 **Cilium**을 사용중

## Cilium 구조

- K8S Cluster 내부에 데몬셋으로 각 노드마다 Cilium Agent가 설치 되어 있습니다. 각 Agent는 노드들의 네트워크 설정을 담당하고 쿠버네티스로 부터 파드 시작과 중지와 같은 event를 수신받으면 이를 eBPF (Bytecode)로 컴파일하여 커널에 적재하는 역할을 수행합니다.

## Cilium 문제점
- Cilium이 제대로 동작하지 못해서 OOM Killed가 된다면 이 중단 시간 동안 동작하는 Pod에 Health check나 재배포시 문제가 생길 수 있습니다.

## 원인 파악

- 우선 메모리 Limit를 상승 시킨다.
- 일정 시점에 GC를 통해서 줄어드는 경우가 있다면 정상적이지만 Cilium의 경우 지속적인 메모리 상승 곡선을 그렸습니다.

## 메모리 누수 디버깅 방법
- 코드 분석
- 메모리 관련 시스템 콜 추적
- Go 프로파일러(runtime/pprof)를 사용

## Cilium 프로파일러

- 애플리케이션을 배포할 때, 런타임 추적이 가능하도록 API가 필요했습니다. Cilium은 기존에 gops라는 프로파일러를 사용하고 있었으며, 이를 통해 메모리 누수 문제를 추적할 수 있었다고 합니다.

```
k -n kube-system exec -it <cilium-pod-name> -c <cilium-container-name> -- gops #(gops 프로파일러 사용)
k -n kube-system exec -it <cilium-pod-name> -c <cilium-container-name> -- gops pprof-heap 1 # http로 통신한 프로파일링 결과를 파일로 저장하였고 이를 메모리 프로파일링으로 실행
go tool pprof <heap_profile_name> # local으로 가져와서 이를 go tool으로 실행하여 시각화
```
- NetlinkAPI 소켓에서 메모리 누수가 발생하는 것을 확인했습니다. 이는 범용 라이브러리 부분에서 문제가 발생했음을 나타냅니다.

## Go Memory 구조 파악

**Code**
```
package main

func useMem(buf []byte) {
	return
}

func memAlloc() []byte {
	sliceA := make([]byte, 100) // sliceA는 상위 함수로 전달되지 않으므로 스택에 할당됨
	useMem(sliceA)

	sliceB := make([]byte, 100) // 빌드 시 바로 이 라인에서 Heap 할당
	useMem(sliceB)

	return sliceB // 상위 함수로 힙에 할당된 sliceB가 할당 됨
}

func main() {
	_ = memAlloc() // 상위 함수에서 반환된 sliceB 사용
}

```
**Command**

```
go build -gcflags="all=-m" goMemory.go
```
**Resoult**

```
./goMemory.go:3:6: can inline useMem
./goMemory.go:7:6: can inline memAlloc
./goMemory.go:9:8: inlining call to useMem
./goMemory.go:12:8: inlining call to useMem
./goMemory.go:17:6: can inline main
./goMemory.go:18:14: inlining call to memAlloc
./goMemory.go:18:14: inlining call to useMem
./goMemory.go:18:14: inlining call to useMem
./goMemory.go:3:13: buf does not escape
./goMemory.go:8:16: make([]byte, 100) does not escape #sliceA (Stack)
./goMemory.go:11:16: make([]byte, 100) escapes to heap #sliceB (Heap)
./goMemory.go:18:14: make([]byte, 100) does not escape
./goMemory.go:18:14: make([]byte, 100) does not escape

```
- go는 하위 함수로의 메모리 전달은 Stack으로 메모리 할당 비용을 줄입니다. (Stack으로 쌓아도 바로 주소값을 따라 갈 수 있음)
- go는 상위 함수로의 메모리 전달은 이전 주소값을 보존 할 수 없으니 Heap으로 할당해버립니다.

# 결론

## 메모리 누수 문제의 원인
위 과정에서 Cilium 메모리 누수의 원인은 NetlinkAPI()를 호출하는 Caller 중 지속적으로 참조하는 객체가 문제 일 것으로 파악하여 이를 카카오에서는 IP / Routing 등 네트워크 정보 캐싱 기능의 버그로 찾았다고 합니다.

**캐시를 비우지 않고 쌓는 것이 원인이였다고 합니다.**

위와 같은 문제의 대응으로 open source PR 후 캐싱 기능을 on/off할 수 있는 옵션을 추가하여, 문제 발생 시 캐시 기능을 비활성화할 수 있도록 개선하여 사용한다고 합니다.

## 문제 해결 절차
### 1. **메모리 사용량 분석**
### 2. **프로파일러를 이용하여 누수 코드 분석**
### 3. **메모리 참조 코드 찾기**

## 느낀점
이번 과정을 통해, 단순히 표면적인 문제 해결이 아닌, 문제의 근본 원인까지 깊이 파고들어 기초를 탄탄히 다지는 것이 얼마나 중요한지를 깨닫게 되었습니다. 우연히 접한 채용 공고를 통해 이 Article을 공부하게 되었는데, 문제를 깊이 있게 분석하고 해결한 뒤 이를 실제 서비스에 반영하는 과정이야말로 진정한 전문가의 길임을 실감했습니다. 단순히 기능을 구현하는 범용적인 개발자에서 벗어나, 시스템의 근본적인 작동 원리와 저변에 깔린 이론을 이해하고, 필요 시 이를 수정하여 최적화하는 과정이 추구해야 할 방향임을 깨달았습니다. 앞으로도 학습이나 실무에서 문제가 발생한다면, 단순히 구현 수준에서의 해결이 아니라, 로우 레벨까지 깊이 이해하고 접근하는 습관을 길러야겠다는 다짐을 하게 되었습니다.


## 참고 자료 (공부 문서)
- [YouTube: KAKAO Cilium 메모리 누수 해결 방법](https://www.youtube.com/watch?v=zWNHG1NSagg)
