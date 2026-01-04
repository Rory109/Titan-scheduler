from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from kubernetes import client, config
import uuid

app = FastAPI()

# 连接k8s集群：加载~/.kube/config配置，让python能像kubectl一样操纵集群
try:
    config.load_kube_config()
except Exception as e:
    print(f"无法连接 k8s 集群：{e}")

# 定义CustomObjectsAPI（专门操纵CRD）
custom_api = client.CustomObjectsApi()

# 定义请求模型
class JobRequest(BaseModel):
    image: str
    gpu_count: int
    priority: int

@app.post("/jobs")
def submit_job(job: JobRequest):
    job_name = f"training-job-{uuid.uuid4().hex[:6]}"

    # 构造CRD对象
    training_job = {
        "apiVersion": "batch.rory109.com/v1",
        "kind": "TrainingJob",
        "metadata":{
            "name": job_name,
            "namespace": "default"
        },
        "spec":{
            "image": job.image,
            "gpuCount":job.gpu_count,
            "priority": job.priority
        }
    }

    try:
        # 调用 k8s API 创建对象cr
        response = custom_api.create_namespaced_custom_object(
            group="batch.rory109.com",
            version="v1",
            namespace="default",
            plural="trainingjobs",
            body=training_job
        )
        return {"message": "Job submitted successfully", "name": job_name,"status":"Queued/Pending"}
    except:
        raise HTTPException(status_code=500, detail=str(e))

@app.get("/jobs")
def list_jobs():
    try:
        # 获取所有任务
        ret = custom_api.list_namespaced_custom_object(
            group="batch.rory109.com",
            version="v1",
            namespace="default",
            plural="trainingjobs"
        )
        jobs = []
        for item in ret['items']:
            jobs.append({
                "name": item['metadata']['name'],
                "status":item.get('status',{}).get('state','Unknown'), #读取controller回写的状态
                "image": item['spec']['image']
            })
        return jobs
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8000)