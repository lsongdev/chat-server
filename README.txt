
curl -X POST http://localhost:8080/conversations \
  -H "Content-Type: application/json" \
  -d '{"conversation_id":"12E823EE-6E69-4EA8-BF7C-DF2133A461DD","members":["hi@lsong.org"]}'

http://localhost:8080/conversations?user=hi@lsong.org
http://localhost:8080/messages?user=hi@lsong.org

curl -X POST http://localhost:8080/messages \
  -H "Content-Type: application/json" \
  -d '{
    "from": "song940@163.com",
    "to": "12E823EE-6E69-4EA8-BF7C-DF2133A461DD",
    "content": "Hello, this is a test message!"
  }'